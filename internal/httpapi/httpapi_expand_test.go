package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/exemt/placitum-geo/internal/store"
)

func expandStore(t *testing.T) *store.Store {
	t.Helper()

	root := filepath.Join("..", "..", "testdata")
	st, err := store.Load(store.Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	if err != nil {
		t.Fatal(err)
	}

	return st
}

type expandReply struct {
	Gen  uint64 `json:"gen"`
	ASNs []struct {
		ASN       uint32                       `json:"asn"`
		Prefix    string                       `json:"prefix"`
		Effective bool                         `json:"effective"`
		Range     *struct{ Start, End string } `json:"range"`
		Prefixes  []string                     `json:"prefixes"`
	} `json:"asns"`
}

func TestGetLookupExpand(t *testing.T) {
	h := Handler(expandStore(t), slog.Default())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/lookup?addr=1.1.1.1&expand=asn", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}

	var got expandReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got.Gen == 0 {
		t.Fatalf("gen missing: %s", rec.Body.String())
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 13335 || !got.ASNs[0].Effective {
		t.Fatalf("asns: %s", rec.Body.String())
	}

	if got.ASNs[0].Prefix != "1.1.1.0/24" || got.ASNs[0].Range == nil || got.ASNs[0].Range.Start != "1.1.1.0" {
		t.Fatalf("effective range: %s", rec.Body.String())
	}

	// Состав CLOUDFLARE из фикстуры: два v4 и один v6.
	if strings.Join(got.ASNs[0].Prefixes, " ") != "1.0.0.0/24 1.1.1.0/24 2606:4700:4700::/48" {
		t.Fatalf("composition: %v", got.ASNs[0].Prefixes)
	}
}

/* Без expand состава нет, а поколение -- есть всегда. */
func TestGetLookupNoExpand(t *testing.T) {
	h := Handler(expandStore(t), slog.Default())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/lookup?addr=1.1.1.1", nil))

	var got expandReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if got.Gen == 0 || len(got.ASNs) != 1 || got.ASNs[0].Prefixes != nil {
		t.Fatalf("plain lookup: %s", rec.Body.String())
	}
}

func TestPostLookupExpand(t *testing.T) {
	h := Handler(expandStore(t), slog.Default())

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/lookup",
		strings.NewReader(`{"addr":"8.8.8.8","expand":"asn"}`)))

	var got expandReply
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	if len(got.ASNs) != 1 || got.ASNs[0].ASN != 15169 || len(got.ASNs[0].Prefixes) != 1 {
		t.Fatalf("post expand: %s", rec.Body.String())
	}
}
