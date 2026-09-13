package load

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exemt/placitum-geo/internal/mmdbtest"
	"github.com/exemt/placitum-geo/internal/table"
)

func TestDirCountry(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "country")
	es, skip, err := Dir(root, KindCountry)
	if err != nil {
		t.Fatal(err)
	}

	if skip != 0 {
		t.Fatalf("skipped=%d", skip)
	}

	if len(es) < 4 {
		t.Fatalf("entries=%d", len(es))
	}

	var us, ru, jp int
	for _, e := range es {
		switch e.Code {
		case "us":
			us++
			if e.Name != "United States" {
				t.Fatalf("us name=%q", e.Name)
			}
		case "ru":
			ru++
			if e.Name != "Russia" {
				t.Fatalf("ru name=%q", e.Name)
			}
		case "jp":
			jp++
			if e.Name != "Japan" {
				t.Fatalf("jp name=%q", e.Name)
			}
		default:
			t.Fatalf("unexpected code %q", e.Code)
		}
	}

	if us == 0 || ru == 0 || jp == 0 {
		t.Fatalf("us=%d ru=%d jp=%d", us, ru, jp)
	}
}

func TestDirASN(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "asn")
	es, _, err := Dir(root, KindASN)
	if err != nil {
		t.Fatal(err)
	}

	seen := map[string]string{}
	for _, e := range es {
		seen[e.Code] = e.Name
	}

	if seen["15169"] != "GOOGLE" || seen["12389"] != "ROSTELECOM" || seen["13335"] != "CLOUDFLARE" || seen["2516"] != "KDDI" {
		t.Fatalf("asns: %+v", seen)
	}
}

func TestTSV(t *testing.T) {
	raw := "us\tv4\t8.8.8.0/24\tUnited States\n" +
		"15169\tv4\t8.8.8.0/24\tGOOGLE\n" +
		"usa\tv4\t1.2.3.0/24\tNope\n"

	es, skip, err := TSV(strings.NewReader(raw), KindCountry)
	if err != nil {
		t.Fatal(err)
	}

	if skip != 2 || len(es) != 1 || es[0].Code != "us" {
		t.Fatalf("country tsv: n=%d skip=%d %+v", len(es), skip, es)
	}

	es, skip, err = TSV(strings.NewReader(raw), KindASN)
	if err != nil {
		t.Fatal(err)
	}

	if skip != 2 || len(es) != 1 || es[0].Code != "15169" {
		t.Fatalf("asn tsv: n=%d skip=%d %+v", len(es), skip, es)
	}
}

/*
 * MMDB -- тот вид, в котором кодер получает выгрузку из панели. База
 * собрана mmdbtest в раскладке GeoLite2: IPv6-дерево, IPv4 под ::/96.
 */
func TestMMDB(t *testing.T) {
	dir := t.TempDir()

	write := func(name string, body []byte) string {
		t.Helper()

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}

		return path
	}

	country := write("GeoLite2-Country.mmdb", mmdbtest.Build("GeoLite2-Country", []mmdbtest.Network{
		{CIDR: "8.8.8.0/24", Record: map[string]any{"country": map[string]any{"iso_code": "US", "names": map[string]any{"en": "United States"}}}},
		{CIDR: "1.0.0.0/24", Record: map[string]any{"registered_country": map[string]any{"iso_code": "AU"}}},
		{CIDR: "2a02:6b8::/32", Record: map[string]any{"country": map[string]any{"iso_code": "RU", "names": map[string]any{"ru": "Россия"}}}},
	}))

	es, skip, err := Path(country, KindCountry)
	if err != nil {
		t.Fatal(err)
	}

	if skip != 0 || len(es) != 3 {
		t.Fatalf("country: n=%d skip=%d %+v", len(es), skip, es)
	}

	tbl := table.Build(es)

	for addr, want := range map[string]string{
		"8.8.8.8":     "us/United States",
		"1.0.0.1":     "au/AU",
		"2a02:6b8::1": "ru/Россия",
	} {
		hit, ok := tbl.Lookup(netip.MustParseAddr(addr))
		if got := hit.Code + "/" + hit.Name; !ok || got != want {
			t.Fatalf("%s: %q ok=%v, want %q", addr, got, ok, want)
		}
	}

	asn := write("GeoLite2-ASN.mmdb", mmdbtest.Build("GeoLite2-ASN", []mmdbtest.Network{
		{CIDR: "8.8.8.0/24", Record: map[string]any{"autonomous_system_number": 15169, "autonomous_system_organization": "GOOGLE"}},
	}))

	es, _, err = Path(asn, KindASN)
	if err != nil {
		t.Fatal(err)
	}

	if len(es) != 1 || es[0].Code != "15169" || es[0].Name != "GOOGLE" {
		t.Fatalf("asn: %+v", es)
	}

	/* База не того вида -- отказ, а не пустой каталог. */
	if _, _, err := Path(asn, KindCountry); err == nil {
		t.Fatal("asn database accepted as country")
	}
}

func TestLabelOf(t *testing.T) {
	code, name, ok := labelOf("US", KindCountry)
	if !ok || code != "us" || name != "US" {
		t.Fatalf("country: %s %s %v", code, name, ok)
	}

	if _, _, ok := labelOf("usa", KindCountry); ok {
		t.Fatal("usa is not iso")
	}

	code, name, ok = labelOf("15169", KindASN)
	if !ok || code != "15169" || name != "AS15169" {
		t.Fatalf("asn: %s %s %v", code, name, ok)
	}
}
