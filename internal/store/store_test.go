package store

import (
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/exemt/placitum-geo/internal/load"
	"github.com/exemt/placitum-geo/internal/mmdbtest"
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

func TestUseSwapsOneKind(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	s, err := Load(Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	before := s.Current()
	path := filepath.Join(t.TempDir(), "country.mmdb")
	body := mmdbtest.Build("GeoLite2-Country", []mmdbtest.Network{{
		CIDR:   "8.8.8.0/24",
		Record: map[string]any{"country": map[string]any{"iso_code": "DE", "names": map[string]any{"en": "Germany"}}},
	}})

	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}

	prev, err := s.Use(load.KindCountry, Source{Path: path, SHA256: "sha256:copy"})
	if err != nil {
		t.Fatal(err)
	}

	if prev.SHA256 != "" || prev.Path != filepath.Join(root, "country") {
		t.Fatalf("previous source: %+v", prev)
	}

	after := s.Current()

	if after.Gen != before.Gen+1 {
		t.Fatalf("generation %d -> %d", before.Gen, after.Gen)
	}

	/* ASN не пересобирался: те же таблицы по указателю. */
	if after.ASN != before.ASN || after.ASNIndex != before.ASNIndex {
		t.Fatal("asn tables were rebuilt for a country swap")
	}

	addr := netip.MustParseAddr("8.8.8.8")

	if hit, ok := after.Country.Lookup(addr); !ok || hit.Code != "de" {
		t.Fatalf("new snapshot: %+v ok=%v", hit, ok)
	}

	/* Снимок, взятый до подмены, отвечает по-прежнему: запрос в полёте не рвётся. */
	if hit, ok := before.Country.Lookup(addr); !ok || hit.Code != "us" {
		t.Fatalf("old snapshot: %+v ok=%v", hit, ok)
	}

	if st := after.Stats(); st.CountrySHA256 != "sha256:copy" || st.ASNSHA256 != "" {
		t.Fatalf("stats: %+v", st)
	}

	/* Опрос диска копию не откатывает: отпечаток считается по её пути. */
	if changed, err := s.Reload(); err != nil || changed {
		t.Fatalf("reload after swap: changed=%v err=%v", changed, err)
	}
}

func TestUseBrokenSourceKeepsSnapshot(t *testing.T) {
	root := filepath.Join("..", "..", "testdata")
	s, err := Load(Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}

	before := s.Current()
	path := filepath.Join(t.TempDir(), "country.mmdb")

	if err := os.WriteFile(path, []byte("not a database"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Use(load.KindCountry, Source{Path: path, SHA256: "sha256:junk"}); err == nil {
		t.Fatal("broken copy accepted")
	}

	if s.Current() != before {
		t.Fatal("broken copy replaced the snapshot")
	}
}
