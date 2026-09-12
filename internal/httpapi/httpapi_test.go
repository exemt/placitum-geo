package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/exemt/placitum-geo/internal/lookup"
	"github.com/exemt/placitum-geo/internal/store"
)

func testHandler(t *testing.T) http.Handler {
	t.Helper()

	root := filepath.Join("..", "..", "testdata")
	st, err := store.Load(store.Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatal(err)
	}

	return Handler(st, slog.Default())
}

func TestGetLookup(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/lookup?addr=8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}

	var got lookup.Result
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 1 || got.Countries[0].Code != "us" {
		t.Fatalf("countries: %+v", got.Countries)
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 15169 {
		t.Fatalf("asns: %+v", got.ASNs)
	}
}

func TestPostCIDR(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string]string{"addr": "0.0.0.0/0"})
	res, err := http.Post(srv.URL+"/lookup", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	var got lookup.Result
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 3 || len(got.ASNs) != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestBadAddr(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/lookup?addr=nope")
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestPostBatch(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string][]string{
		"addrs": {"8.8.8.8", "nope", "0.0.0.0/0"},
	})
	res, err := http.Post(srv.URL+"/lookup/batch", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}

	var got struct {
		Results []lookup.BatchItem `json:"results"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}

	if len(got.Results) != 3 {
		t.Fatalf("results: %+v", got.Results)
	}

	if got.Results[0].Addr != "8.8.8.8" || len(got.Results[0].Countries) != 1 || got.Results[0].Countries[0].Code != "us" {
		t.Fatalf("item 0: %+v", got.Results[0])
	}

	if got.Results[1].Error == "" {
		t.Fatalf("item 1 should carry an error: %+v", got.Results[1])
	}

	if len(got.Results[2].Countries) != 3 || len(got.Results[2].ASNs) != 4 {
		t.Fatalf("item 2: %+v", got.Results[2])
	}
}

func TestPostBatchEmpty(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	body, _ := json.Marshal(map[string][]string{"addrs": {}})
	res, err := http.Post(srv.URL+"/lookup/batch", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestPostBatchTooMany(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	addrs := make([]string, MaxBatch+1)
	for i := range addrs {
		addrs[i] = "8.8.8.8"
	}

	body, _ := json.Marshal(map[string][]string{"addrs": addrs})
	res, err := http.Post(srv.URL+"/lookup/batch", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status=%d", res.StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer(testHandler(t))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}

	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", res.StatusCode)
	}
}
