package store

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadLookup(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	s, err := Load(Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	snap := s.Current()
	if snap.Country.Len() == 0 || snap.ASN.Len() == 0 {
		t.Fatalf("empty tables: %+v", snap.Stats())
	}

	hit, ok := snap.Country.Lookup(netip.MustParseAddr("8.8.8.8"))
	if !ok || hit.Code != "us" {
		t.Fatalf("country: %+v ok=%v", hit, ok)
	}

	hit, ok = snap.ASN.Lookup(netip.MustParseAddr("5.8.8.10"))
	if !ok || hit.Code != "12389" {
		t.Fatalf("asn: %+v ok=%v", hit, ok)
	}
}

func TestReloadUnchanged(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	s, err := Load(Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	gen := s.gen.Load()

	for i := 0; i < 3; i++ {
		changed, err := s.Reload()
		if err != nil {
			t.Fatal(err)
		}

		if changed {
			t.Fatal("same files must not rotate the snapshot")
		}
	}

	/* Поколение растёт только в build: неизменные файлы не пересобираются. */
	if got := s.gen.Load(); got != gen {
		t.Fatalf("unchanged files were rebuilt: generation %d -> %d", gen, got)
	}
}

func TestMissingPath(t *testing.T) {
	_, err := Load(Paths{Country: filepath.Join(os.TempDir(), "no-such-geo"), ASN: ""}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
}
