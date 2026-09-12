package grpcapi

import (
	"context"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	"github.com/exemt/placitum-geo/internal/store"
	geopb "github.com/exemt/placitum-geo/proto"
)

func testClient(t *testing.T) geopb.GeoClient {
	t.Helper()

	root := filepath.Join("..", "..", "testdata")
	st, err := store.Load(store.Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatal(err)
	}

	lis := bufconn.Listen(1024 * 1024)
	gs := grpc.NewServer()
	geopb.RegisterGeoServer(gs, New(st))

	go func() {
		_ = gs.Serve(lis)
	}()

	t.Cleanup(func() {
		gs.Stop()
		_ = lis.Close()
	})

	conn, err := grpc.NewClient("passthrough://bufnet",
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = conn.Close() })

	return geopb.NewGeoClient(conn)
}

func TestLookupIP(t *testing.T) {
	c := testClient(t)

	got, err := c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "8.8.8.8"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 1 || got.Countries[0].Code != "us" {
		t.Fatalf("countries: %+v", got.Countries)
	}

	if len(got.Asns) != 1 || got.Asns[0].Asn != 15169 {
		t.Fatalf("asns: %+v", got.Asns)
	}
}

func TestLookupCIDR(t *testing.T) {
	c := testClient(t)

	got, err := c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "0.0.0.0/0"})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 3 || len(got.Asns) != 4 {
		t.Fatalf("got %+v", got)
	}
}

func TestLookupBadAddr(t *testing.T) {
	c := testClient(t)

	_, err := c.Lookup(context.Background(), &geopb.LookupRequest{Addr: "nope"})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code=%v err=%v", status.Code(err), err)
	}
}
