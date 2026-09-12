package load

import (
	"path/filepath"
	"strings"
	"testing"
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
