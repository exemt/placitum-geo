package table

import (
	"net/netip"
	"testing"
)

/*
 * Вложенные анонсы: узкий /24 внутри широкого /9 другой системы. Сплющенная
 * таблица помнит только победителя, индекс -- обоих, узкий первым.
 */
func TestIndexCoveringNested(t *testing.T) {
	x := BuildIndex([]Entry{
		entry("8.0.0.0/9", "3356", "LEVEL3"),
		entry("8.8.8.0/24", "15169", "GOOGLE"),
		entry("1.1.1.0/24", "13335", "CLOUDFLARE"),
	})

	got := x.Covering(netip.MustParseAddr("8.8.8.143"))
	if len(got) != 2 {
		t.Fatalf("covering: %+v", got)
	}

	if got[0].Code != "15169" || got[0].Prefix.String() != "8.8.8.0/24" {
		t.Fatalf("narrowest first: %+v", got[0])
	}

	if got[1].Code != "3356" || got[1].Prefix.String() != "8.0.0.0/9" {
		t.Fatalf("wider second: %+v", got[1])
	}

	// Адрес из /9, но вне /24: только широкий.
	got = x.Covering(netip.MustParseAddr("8.9.9.9"))
	if len(got) != 1 || got[0].Code != "3356" {
		t.Fatalf("outside /24: %+v", got)
	}

	if got := x.Covering(netip.MustParseAddr("9.9.9.9")); len(got) != 0 {
		t.Fatalf("miss: %+v", got)
	}
}

/*
 * MOAS: одна сеть, две системы. Обе в ответе, по коду; повтор той же пары
 * «префикс + код» -- не второй анонс.
 */
func TestIndexCoveringMOAS(t *testing.T) {
	x := BuildIndex([]Entry{
		entry("203.0.113.0/24", "64500", "B"),
		entry("203.0.113.0/24", "64496", "A"),
		entry("203.0.113.0/24", "64496", "A"),
	})

	got := x.Covering(netip.MustParseAddr("203.0.113.7"))
	if len(got) != 2 || got[0].Code != "64496" || got[1].Code != "64500" {
		t.Fatalf("moas: %+v", got)
	}
}

/* Состав системы: отсортирован, без повторов, чужие сети не попадают. */
func TestIndexPrefixes(t *testing.T) {
	x := BuildIndex([]Entry{
		entry("8.8.8.0/24", "15169", "GOOGLE"),
		entry("8.8.4.0/24", "15169", "GOOGLE"),
		entry("8.8.4.0/24", "15169", "GOOGLE"),
		entry("2001:4860::/32", "15169", "GOOGLE"),
		entry("1.1.1.0/24", "13335", "CLOUDFLARE"),
	})

	got := x.Prefixes("15169")
	if len(got) != 3 {
		t.Fatalf("prefixes: %v", got)
	}

	if got[0].String() != "8.8.4.0/24" || got[1].String() != "8.8.8.0/24" || got[2].String() != "2001:4860::/32" {
		t.Fatalf("order: %v", got)
	}

	if x.Name("15169") != "GOOGLE" || x.Name("1") != "" {
		t.Fatalf("names: %q %q", x.Name("15169"), x.Name("1"))
	}

	if got := x.Prefixes("1"); got != nil {
		t.Fatalf("unknown code: %v", got)
	}

	if x.Codes() != 2 {
		t.Fatalf("codes: %d", x.Codes())
	}
}

/* v4 внутри v6 -- та же сеть, что и v4: один ключ, один стаб. */
func TestIndexMappedV4(t *testing.T) {
	x := BuildIndex([]Entry{
		{Prefix: netip.MustParsePrefix("::ffff:8.8.8.0/120"), Code: "15169", Name: "GOOGLE"},
		entry("8.8.8.0/24", "15169", "GOOGLE"),
	})

	got := x.Covering(netip.MustParseAddr("8.8.8.8"))
	if len(got) != 1 || got[0].Prefix.String() != "8.8.8.0/24" {
		t.Fatalf("mapped: %+v", got)
	}

	if got := x.Covering(netip.MustParseAddr("::ffff:8.8.8.8")); len(got) != 1 {
		t.Fatalf("mapped addr: %+v", got)
	}
}

func TestIndexNil(t *testing.T) {
	var x *Index

	if got := x.Covering(netip.MustParseAddr("8.8.8.8")); got != nil {
		t.Fatalf("nil index: %+v", got)
	}

	if x.Prefixes("15169") != nil || x.Name("15169") != "" || x.Codes() != 0 {
		t.Fatal("nil index must be empty")
	}
}
