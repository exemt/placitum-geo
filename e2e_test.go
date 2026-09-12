/*
 * Сквозной прогон API: поднимаем cmd/geo и бьём в HTTP и gRPC снаружи.
 * Те же учебные выгрузки, что и у внутренних тестов.
 */

package geo_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	geopb "github.com/exemt/placitum-geo/proto"
)

type result struct {
	Countries []struct {
		Code string `json:"code"`
		Name string `json:"name"`
	} `json:"countries"`
	ASNs []struct {
		ASN  uint32 `json:"asn"`
		Name string `json:"name"`
	} `json:"asns"`
}

type want struct {
	countries []string
	asns      []uint32
}

var cases = []struct {
	name string
	addr string
	want want
}{
	{"google", "8.8.8.8", want{[]string{"us"}, []uint32{15169}}},
	{"cloudflare", "1.1.1.1", want{[]string{"us"}, []uint32{13335}}},
	{"rostelecom", "5.8.8.10", want{[]string{"ru"}, []uint32{12389}}},
	{"kddi", "133.1.2.3", want{[]string{"jp"}, []uint32{2516}}},
	{"unknown", "9.9.9.9", want{}},
	{"cidr24", "8.8.8.0/24", want{[]string{"us"}, []uint32{15169}}},
	{"world", "0.0.0.0/0", want{[]string{"jp", "ru", "us"}, []uint32{2516, 12389, 13335, 15169}}},
	{"ipv6", "2a02:6b8:1::1", want{[]string{"ru"}, []uint32{12389}}},
	{"mapped", "::ffff:8.8.8.8", want{[]string{"us"}, []uint32{15169}}},
}

func TestAPI(t *testing.T) {
	httpURL, grpcAddr := start(t)
	client := grpcClient(t, grpcAddr)

	t.Run("healthz", func(t *testing.T) {
		res, err := http.Get(httpURL + "/healthz")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()

		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != http.StatusOK || string(body) != "ok\n" {
			t.Fatalf("status=%d body=%q", res.StatusCode, body)
		}
	})

	for _, tc := range cases {
		t.Run("http/get/"+tc.name, func(t *testing.T) {
			got := httpGET(t, httpURL+"/lookup?"+url.Values{"addr": {tc.addr}}.Encode(), http.StatusOK)
			assertResult(t, got, tc.want)
		})
		t.Run("http/post/"+tc.name, func(t *testing.T) {
			got := httpPOST(t, httpURL+"/lookup", map[string]string{"addr": tc.addr}, http.StatusOK)
			assertResult(t, got, tc.want)
		})
		t.Run("grpc/"+tc.name, func(t *testing.T) {
			got, err := client.Lookup(context.Background(), &geopb.LookupRequest{Addr: tc.addr})
			if err != nil {
				t.Fatal(err)
			}
			assertGRPC(t, got, tc.want)
		})
	}

	t.Run("http/get/ip-alias", func(t *testing.T) {
		got := httpGET(t, httpURL+"/lookup?"+url.Values{"ip": {"8.8.8.8"}}.Encode(), http.StatusOK)
		assertResult(t, got, want{[]string{"us"}, []uint32{15169}})
	})

	t.Run("http/post/ip-alias", func(t *testing.T) {
		got := httpPOST(t, httpURL+"/lookup", map[string]string{"ip": "5.8.8.10"}, http.StatusOK)
		assertResult(t, got, want{[]string{"ru"}, []uint32{12389}})
	})

	t.Run("http/bad-addr", func(t *testing.T) {
		httpGET(t, httpURL+"/lookup?"+url.Values{"addr": {"nope"}}.Encode(), http.StatusBadRequest)
	})

	t.Run("http/empty", func(t *testing.T) {
		httpGET(t, httpURL+"/lookup", http.StatusBadRequest)
	})

	t.Run("http/bad-json", func(t *testing.T) {
		res, err := http.Post(httpURL+"/lookup", "application/json", bytes.NewReader([]byte("{")))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()

		if res.StatusCode != http.StatusBadRequest {
			t.Fatalf("status=%d", res.StatusCode)
		}
	})

	t.Run("grpc/bad-addr", func(t *testing.T) {
		_, err := client.Lookup(context.Background(), &geopb.LookupRequest{Addr: "nope"})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v err=%v", status.Code(err), err)
		}
	})

	t.Run("grpc/empty", func(t *testing.T) {
		_, err := client.Lookup(context.Background(), &geopb.LookupRequest{})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code=%v err=%v", status.Code(err), err)
		}
	})
}

func start(t *testing.T) (httpURL, grpcAddr string) {
	t.Helper()

	root := moduleRoot(t)
	bin := filepath.Join(t.TempDir(), "waf-geo")
	if runtime.GOOS == "windows" {
		bin += ".exe"
	}

	build := exec.Command("go", "build", "-o", bin, "./cmd/geo")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	httpAddr := freePort(t)
	grpcAddr = freePort(t)

	cmd := exec.Command(bin)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"WAF_GEO_COUNTRY="+filepath.Join(root, "testdata", "country"),
		"WAF_GEO_ASN="+filepath.Join(root, "testdata", "asn"),
		"WAF_GEO_HTTP="+httpAddr,
		"WAF_GEO_GRPC="+grpcAddr,
		"WAF_GEO_LOG=error",
		"WAF_GEO_RELOAD_EVERY=1h",
		"WAF_NATS_URL=",
	)

	var logs bytes.Buffer
	cmd.Stdout = &logs
	cmd.Stderr = &logs

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if t.Failed() && logs.Len() > 0 {
			t.Logf("process log:\n%s", logs.String())
		}
	})

	httpURL = "http://" + httpAddr
	waitHTTP(t, httpURL+"/healthz", &logs)

	return httpURL, grpcAddr
}

func grpcClient(t *testing.T, addr string) geopb.GeoClient {
	t.Helper()

	var last error
	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			last = err
			time.Sleep(50 * time.Millisecond)
			continue
		}

		c := geopb.NewGeoClient(conn)
		_, err = c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "9.9.9.9"})
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return c
		}

		last = err
		_ = conn.Close()
		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("grpc: %v", last)
	return nil
}

func httpGET(t *testing.T, url string, status int) result {
	t.Helper()

	res, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	return decodeHTTP(t, res, status)
}

func httpPOST(t *testing.T, url string, body map[string]string, status int) result {
	t.Helper()

	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}

	res, err := http.Post(url, "application/json", bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	return decodeHTTP(t, res, status)
}

func decodeHTTP(t *testing.T, res *http.Response, status int) result {
	t.Helper()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != status {
		t.Fatalf("status=%d want=%d body=%s", res.StatusCode, status, raw)
	}

	if status != http.StatusOK {
		return result{}
	}

	var got result
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("json: %v body=%s", err, raw)
	}

	return got
}

func assertResult(t *testing.T, got result, w want) {
	t.Helper()

	codes := make([]string, 0, len(got.Countries))
	for _, c := range got.Countries {
		codes = append(codes, c.Code)
	}

	asns := make([]uint32, 0, len(got.ASNs))
	for _, a := range got.ASNs {
		asns = append(asns, a.ASN)
	}

	if !eqStrings(codes, w.countries) {
		t.Fatalf("countries=%v want=%v", codes, w.countries)
	}

	if !eqUint32(asns, w.asns) {
		t.Fatalf("asns=%v want=%v", asns, w.asns)
	}
}

func assertGRPC(t *testing.T, got *geopb.LookupResponse, w want) {
	t.Helper()

	codes := make([]string, 0, len(got.GetCountries()))
	for _, c := range got.GetCountries() {
		codes = append(codes, c.GetCode())
	}

	asns := make([]uint32, 0, len(got.GetAsns()))
	for _, a := range got.GetAsns() {
		asns = append(asns, a.GetAsn())
	}

	if !eqStrings(codes, w.countries) {
		t.Fatalf("countries=%v want=%v", codes, w.countries)
	}

	if !eqUint32(asns, w.asns) {
		t.Fatalf("asns=%v want=%v", asns, w.asns)
	}
}

func waitHTTP(t *testing.T, url string, logs *bytes.Buffer) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
	var last error

	for time.Now().Before(deadline) {
		res, err := http.Get(url)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return
			}
			last = fmt.Errorf("status %d", res.StatusCode)
		} else {
			last = err
		}

		time.Sleep(50 * time.Millisecond)
	}

	t.Fatalf("http not ready: %v\n%s", last, logs.String())
}

func freePort(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	addr := ln.Addr().String()
	_ = ln.Close()

	return addr
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}

	return filepath.Dir(file)
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}

func eqUint32(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
