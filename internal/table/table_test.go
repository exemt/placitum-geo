package table

import (
	"net/netip"
	"testing"
)

func mustPrefix(s string) netip.Prefix {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked()
	}

	a := netip.MustParseAddr(s)

	return netip.PrefixFrom(a, a.BitLen())
}

func entry(prefix, code, name string) Entry {
	return Entry{Prefix: mustPrefix(prefix), Code: code, Name: name}
}

func TestLookupIPv4(t *testing.T) {
	tab := Build([]Entry{
		entry("10.0.0.0/8", "us", "United States"),
		entry("192.168.1.5", "ru", "Russia"),
		entry("203.0.113.0/24", "jp", "Japan"),
	})

	hit, ok := tab.Lookup(netip.MustParseAddr("10.1.2.3"))
	if !ok || hit.Code != "us" || hit.Name != "United States" {
		t.Fatalf("10.1.2.3: %+v ok=%v", hit, ok)
	}

	hit, ok = tab.Lookup(netip.MustParseAddr("192.168.1.5"))
	if !ok || hit.Code != "ru" {
		t.Fatalf("192.168.1.5: %+v ok=%v", hit, ok)
	}

	if _, ok := tab.Lookup(netip.MustParseAddr("192.168.1.6")); ok {
		t.Fatal("192.168.1.6 must miss")
	}

	if _, ok := tab.Lookup(netip.MustParseAddr("8.8.8.8")); ok {
		t.Fatal("8.8.8.8 must miss")
	}
}

func TestLookupMostSpecific(t *testing.T) {
	tab := Build([]Entry{
		entry("10.0.0.0/8", "us", ""),
		entry("10.2.0.0/16", "ru", ""),
	})

	hit, ok := tab.Lookup(netip.MustParseAddr("10.1.1.1"))
	if !ok || hit.Code != "us" {
		t.Fatalf("10.1.1.1: %+v ok=%v", hit, ok)
	}

	hit, ok = tab.Lookup(netip.MustParseAddr("10.2.3.4"))
	if !ok || hit.Code != "ru" {
		t.Fatalf("10.2.3.4: %+v ok=%v", hit, ok)
	}
}

func TestLookupIPv6(t *testing.T) {
	tab := Build([]Entry{
		entry("2a02:6b8::/32", "ru", "Russia"),
		entry("2001:db8::1", "us", ""),
	})

	hit, ok := tab.Lookup(netip.MustParseAddr("2a02:6b8:1::1"))
	if !ok || hit.Code != "ru" {
		t.Fatalf("expected ru, got %+v ok=%v", hit, ok)
	}

	if _, ok := tab.Lookup(netip.MustParseAddr("2001:db8::2")); ok {
		t.Fatal("expected miss")
	}
}

func TestUnmapIPv4Mapped(t *testing.T) {
	tab := Build([]Entry{entry("8.8.8.0/24", "us", "")})

	hit, ok := tab.Lookup(netip.MustParseAddr("::ffff:8.8.8.8"))
	if !ok || hit.Code != "us" {
		t.Fatalf("mapped: %+v ok=%v", hit, ok)
	}
}

func TestCoveringCIDR(t *testing.T) {
	tab := Build([]Entry{
		entry("8.8.8.0/24", "us", "United States"),
		entry("5.8.8.0/24", "ru", "Russia"),
		entry("95.24.0.0/16", "ru", "Russia"),
	})

	hits := tab.Covering(mustPrefix("8.8.8.8"))
	if len(hits) != 1 || hits[0].Code != "us" {
		t.Fatalf("host: %+v", hits)
	}

	hits = tab.Covering(mustPrefix("8.8.8.0/24"))
	if len(hits) != 1 || hits[0].Code != "us" {
		t.Fatalf("/24: %+v", hits)
	}

	hits = tab.Covering(mustPrefix("0.0.0.0/0"))
	if len(hits) != 2 || hits[0].Code != "ru" || hits[1].Code != "us" {
		t.Fatalf("world: %+v", hits)
	}

	hits = tab.Covering(mustPrefix("9.9.9.0/24"))
	if len(hits) != 0 {
		t.Fatalf("empty: %+v", hits)
	}
}

func TestCoveringNested(t *testing.T) {
	tab := Build([]Entry{
		entry("10.0.0.0/8", "us", ""),
		entry("10.2.0.0/16", "ru", ""),
	})

	hits := tab.Covering(mustPrefix("10.0.0.0/8"))
	if len(hits) != 2 || hits[0].Code != "ru" || hits[1].Code != "us" {
		t.Fatalf("cover /8: %+v", hits)
	}

	hits = tab.Covering(mustPrefix("10.2.0.0/16"))
	if len(hits) != 1 || hits[0].Code != "ru" {
		t.Fatalf("cover /16: %+v", hits)
	}
}

/*
 * Соседние анонсы одного кода больше не склеиваются: корзинам капчи нужен
 * именно анонс, и два /25 -- это две сети, а не один /24. Склейка осталась
 * только кускам одного и того же анонса, разрезанного пересечением.
 */
func TestAdjacentSameCodeKeepTheirPrefixes(t *testing.T) {
	tab := Build([]Entry{
		entry("8.8.8.0/25", "us", "A"),
		entry("8.8.8.128/25", "us", "A"),
	})

	if tab.Len() != 2 {
		t.Fatalf("len=%d, want two announcements", tab.Len())
	}

	hit, ok := tab.Lookup(netip.MustParseAddr("8.8.8.200"))
	if !ok || hit.Code != "us" || hit.Prefix.String() != "8.8.8.128/25" {
		t.Fatalf("upper half: %+v ok=%v", hit, ok)
	}

	hit, ok = tab.Lookup(netip.MustParseAddr("8.8.8.1"))
	if !ok || hit.Prefix.String() != "8.8.8.0/25" {
		t.Fatalf("lower half: %+v ok=%v", hit, ok)
	}
}

func TestParseASN(t *testing.T) {
	n, ok := ParseASN("15169")
	if !ok || n != 15169 {
		t.Fatalf("got %d %v", n, ok)
	}

	if _, ok := ParseASN("0"); ok {
		t.Fatal("0 is not an asn")
	}

	if _, ok := ParseASN("us"); ok {
		t.Fatal("us is not an asn")
	}
}
