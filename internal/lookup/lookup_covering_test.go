package lookup

import (
	"net/netip"
	"testing"

	"github.com/exemt/placitum-geo/internal/store"
	"github.com/exemt/placitum-geo/internal/table"
)

func entry(prefix, code, name string) table.Entry {
	return table.Entry{Prefix: netip.MustParsePrefix(prefix).Masked(), Code: code, Name: name}
}

/*
 * Снимок с перекрытиями: узкий /24 внутри широкого /9 другой системы и MOAS
 * на третьей сети. Из фикстур такого не собрать -- они плоские.
 */
func nested() *store.Snapshot {
	asn := []table.Entry{
		entry("8.0.0.0/9", "3356", "LEVEL3"),
		entry("8.8.8.0/24", "15169", "GOOGLE"),
		entry("8.8.4.0/24", "15169", "GOOGLE"),
		entry("203.0.113.0/24", "64496", "A"),
		entry("203.0.113.0/24", "64500", "B"),
	}

	return &store.Snapshot{
		Gen:      7,
		Country:  table.Build(nil),
		ASN:      table.Build(asn),
		ASNIndex: table.BuildIndex(asn),
	}
}

func TestDoAddrCoveringNested(t *testing.T) {
	got, err := Do(nested(), "8.8.8.143")
	if err != nil {
		t.Fatal(err)
	}

	if got.Gen != 7 {
		t.Fatalf("gen: %d", got.Gen)
	}

	if len(got.ASNs) != 2 {
		t.Fatalf("asns: %+v", got.ASNs)
	}

	first := got.ASNs[0]
	if first.ASN != 15169 || first.Prefix != "8.8.8.0/24" || !first.Effective {
		t.Fatalf("effective first: %+v", first)
	}

	if first.Range == nil || first.Range.Start != "8.8.8.0" || first.Range.End != "8.8.8.255" {
		t.Fatalf("range: %+v", first.Range)
	}

	second := got.ASNs[1]
	if second.ASN != 3356 || second.Prefix != "8.0.0.0/9" || second.Effective || second.Range != nil {
		t.Fatalf("wider second, not effective: %+v", second)
	}

	// Без просьбы состава нет.
	if first.Prefixes != nil || second.Prefixes != nil {
		t.Fatalf("no expand: %+v", got.ASNs)
	}
}

/* Адрес широкой системы вне узкого анонса: кусок обрезан узким соседом. */
func TestDoAddrEffectiveRangeCut(t *testing.T) {
	got, err := Do(nested(), "8.8.9.1")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 3356 || !got.ASNs[0].Effective {
		t.Fatalf("asns: %+v", got.ASNs)
	}

	// От конца 8.8.8.0/24 до начала следующего узкого -- 8.8.4.0/24 ниже,
	// значит кусок тянется от 8.8.9.0 до конца /9.
	r := got.ASNs[0].Range
	if r == nil || r.Start != "8.8.9.0" || r.End != "8.127.255.255" {
		t.Fatalf("range: %+v", r)
	}
}

func TestDoAddrMOAS(t *testing.T) {
	got, err := Do(nested(), "203.0.113.7")
	if err != nil {
		t.Fatal(err)
	}

	if len(got.ASNs) != 2 {
		t.Fatalf("asns: %+v", got.ASNs)
	}

	// Побеждает меньший код -- так решает сплющенная таблица; он и первый.
	if got.ASNs[0].ASN != 64496 || !got.ASNs[0].Effective || got.ASNs[1].ASN != 64500 || got.ASNs[1].Effective {
		t.Fatalf("moas order: %+v", got.ASNs)
	}

	if got.ASNs[1].Prefix != "203.0.113.0/24" {
		t.Fatalf("moas prefix: %+v", got.ASNs[1])
	}
}

func TestDoExpand(t *testing.T) {
	got, err := DoWith(nested(), "8.8.8.143", Options{ExpandASN: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.ASNs) != 2 {
		t.Fatalf("asns: %+v", got.ASNs)
	}

	google := got.ASNs[0].Prefixes
	if len(google) != 2 || google[0] != "8.8.4.0/24" || google[1] != "8.8.8.0/24" {
		t.Fatalf("google composition: %v", google)
	}

	level3 := got.ASNs[1].Prefixes
	if len(level3) != 1 || level3[0] != "8.0.0.0/9" {
		t.Fatalf("level3 composition: %v", level3)
	}
}

/* Диапазон отвечает как прежде: каждая система один раз, по номеру. */
func TestDoRangeUnchanged(t *testing.T) {
	got, err := DoWith(nested(), "8.0.0.0/8", Options{ExpandASN: true})
	if err != nil {
		t.Fatal(err)
	}

	if len(got.ASNs) != 2 || got.ASNs[0].ASN != 3356 || got.ASNs[1].ASN != 15169 {
		t.Fatalf("range: %+v", got.ASNs)
	}

	for _, row := range got.ASNs {
		if row.Effective || row.Range != nil {
			t.Fatalf("range rows carry no effective mark: %+v", row)
		}

		if len(row.Prefixes) == 0 {
			t.Fatalf("expand on range: %+v", row)
		}
	}
}

func TestDoNilSnapshot(t *testing.T) {
	got, err := DoWith(nil, "8.8.8.8", Options{ExpandASN: true})
	if err != nil {
		t.Fatal(err)
	}

	if got.Gen != 0 || len(got.ASNs) != 0 || len(got.Countries) != 0 {
		t.Fatalf("nil snapshot: %+v", got)
	}
}
