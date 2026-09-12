package lookup

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/exemt/placitum-geo/internal/store"
)

func testdata(t *testing.T) *store.Snapshot {
	t.Helper()

	root := filepath.Join("..", "..", "testdata")
	s, err := store.Load(store.Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatal(err)
	}

	return s.Current()
}

func TestDoIP(t *testing.T) {
	snap := testdata(t)

	got, err := Do(snap, "8.8.8.8")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 1 || got.Countries[0].Code != "us" || got.Countries[0].Name != "United States" {
		t.Fatalf("countries: %+v", got.Countries)
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 15169 || got.ASNs[0].Name != "GOOGLE" {
		t.Fatalf("asns: %+v", got.ASNs)
	}
}

func TestDoUnknown(t *testing.T) {
	snap := testdata(t)

	got, err := Do(snap, "9.9.9.9")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 0 || len(got.ASNs) != 0 {
		t.Fatalf("want empty, got %+v", got)
	}
}

func TestDoCIDR(t *testing.T) {
	snap := testdata(t)

	got, err := Do(snap, "0.0.0.0/0")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 3 || got.Countries[0].Code != "jp" || got.Countries[1].Code != "ru" || got.Countries[2].Code != "us" {
		t.Fatalf("countries: %+v", got.Countries)
	}

	if len(got.ASNs) != 4 || got.ASNs[0].ASN != 2516 || got.ASNs[1].ASN != 12389 || got.ASNs[2].ASN != 13335 || got.ASNs[3].ASN != 15169 {
		t.Fatalf("asns: %+v", got.ASNs)
	}
}

func TestDoIPv6(t *testing.T) {
	snap := testdata(t)

	got, err := Do(snap, "2a02:6b8:1::1")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Countries) != 1 || got.Countries[0].Code != "ru" {
		t.Fatalf("countries: %+v", got.Countries)
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 12389 {
		t.Fatalf("asns: %+v", got.ASNs)
	}
}

func TestParse(t *testing.T) {
	p, err := Parse("8.8.8.8")
	if err != nil || p.Bits() != 32 || p.Addr().String() != "8.8.8.8" {
		t.Fatalf("ip: %v %v", p, err)
	}

	p, err = Parse("8.8.8.0/24")
	if err != nil || p.Bits() != 24 {
		t.Fatalf("cidr: %v %v", p, err)
	}

	if _, err := Parse("not-an-ip"); err == nil {
		t.Fatal("expected error")
	}

	if _, err := Parse(""); err == nil {
		t.Fatal("expected empty error")
	}
}
