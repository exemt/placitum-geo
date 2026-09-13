package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/exemt/placitum-geo/internal/load"
	"github.com/exemt/placitum-geo/internal/mmdbtest"
	"github.com/exemt/placitum-geo/internal/store"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func countryDB(code string) []byte {
	return mmdbtest.Build("GeoLite2-Country", []mmdbtest.Network{{
		CIDR:   "8.8.8.0/24",
		Record: map[string]any{"country": map[string]any{"iso_code": code}},
	}})
}

func shaOf(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func fileOf(b []byte) *File {
	return &File{SHA256: shaOf(b), Size: int64(len(b)), Type: "GeoLite2-Country", Build: mmdbtest.BuildEpoch}
}

// controller -- контроллер с одним файлом на вид.
type controller struct {
	mu     sync.Mutex
	files  map[string][]byte
	calls  int
	during func()
}

func (c *controller) set(kind string, body []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.files[kind] = body
}

// onServe -- зовётся, пока копия ещё не отдана.
func (c *controller) onServe(fn func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.during = fn
}

func (c *controller) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func (c *controller) serve(t *testing.T) string {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		body, ok := c.files[strings.TrimPrefix(r.URL.Path, "/api/geo/files/")]
		during := c.during
		c.calls++
		c.mu.Unlock()

		if !ok {
			http.NotFound(w, r)
			return
		}

		if during != nil {
			during()
		}

		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	return srv.URL
}

func testStore(t *testing.T) *store.Store {
	t.Helper()

	root := filepath.Join("..", "..", "testdata")
	st, err := store.Load(store.Paths{
		Country: filepath.Join(root, "country"),
		ASN:     filepath.Join(root, "asn"),
	}, quiet)
	if err != nil {
		t.Fatal(err)
	}

	return st
}

func codeAt(snap *store.Snapshot, addr string) string {
	hit, _ := snap.Country.Lookup(netip.MustParseAddr(addr))
	return hit.Code
}

func names(t *testing.T, dir string) []string {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}

	return out
}

func TestApplyDownloadsThenSwapsAndDropsOldCopy(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	first, second := countryDB("DE"), countryDB("FR")
	ctrl := &controller{files: map[string][]byte{"country": first}}
	url := ctrl.serve(t)
	before := st.Current()

	var (
		seenMu   sync.Mutex
		seenGen  uint64
		seenCode string
	)

	ctrl.onServe(func() {
		snap := st.Current()
		seenMu.Lock()
		seenGen, seenCode = snap.Gen, codeAt(snap, "8.8.8.8")
		seenMu.Unlock()
	})

	s := New(dir, url, st, quiet)

	if err := s.Apply(context.Background(), &Doc{V: 1, Kind: DocKind, Rev: 1, SHA256: "sha256:one", Country: fileOf(first)}); err != nil {
		t.Fatal(err)
	}

	/* Пока копия качалась, кодер отвечал по прежнему снимку. */
	seenMu.Lock()
	if seenGen != before.Gen || seenCode != "us" {
		t.Fatalf("while downloading: gen=%d code=%q, want gen=%d us", seenGen, seenCode, before.Gen)
	}
	seenMu.Unlock()

	after := st.Current()

	if code := codeAt(after, "8.8.8.8"); code != "de" {
		t.Fatalf("after swap: %q", code)
	}

	if after.ASN != before.ASN {
		t.Fatal("asn tables were rebuilt for a country copy")
	}

	src := after.Source(load.KindCountry)
	if src.SHA256 != shaOf(first) {
		t.Fatalf("source: %+v", src)
	}

	ctrl.onServe(nil)
	ctrl.set("country", second)

	if err := s.Apply(context.Background(), &Doc{V: 1, Kind: DocKind, Rev: 2, SHA256: "sha256:two", Country: fileOf(second)}); err != nil {
		t.Fatal(err)
	}

	if code := codeAt(st.Current(), "8.8.8.8"); code != "fr" {
		t.Fatalf("second swap: %q", code)
	}

	if _, err := os.Stat(src.Path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("old copy is still on disk: %v", err)
	}

	if got := names(t, dir); len(got) != 1 {
		t.Fatalf("copies on disk: %v", got)
	}

	/* Документ о том, что уже стоит, к контроллеру не ведёт. */
	calls := ctrl.count()

	if err := s.Apply(context.Background(), &Doc{V: 1, Kind: DocKind, Rev: 2, SHA256: "sha256:two", Country: fileOf(second)}); err != nil {
		t.Fatal(err)
	}

	if ctrl.count() != calls {
		t.Fatal("the copy in use was fetched again")
	}
}

func TestApplyKeepsSnapshotWhenCopyDoesNotArrive(t *testing.T) {
	st := testStore(t)
	dir := t.TempDir()
	body := countryDB("DE")
	ctrl := &controller{files: map[string][]byte{"country": body}}
	before := st.Current()

	/* Контроллер уже держит другой файл, чем называет документ. */
	s := New(dir, ctrl.serve(t), st, quiet)
	stale := &Doc{V: 1, Kind: DocKind, Rev: 1, SHA256: "sha256:x", Country: &File{SHA256: shaOf([]byte("another file")), Size: int64(len(body))}}

	if err := s.Apply(context.Background(), stale); err == nil {
		t.Fatal("hash mismatch accepted")
	}

	/* Контроллер не отвечает вовсе. */
	dead := New(dir, "http://127.0.0.1:1", st, quiet)

	if err := dead.Apply(context.Background(), &Doc{V: 1, Kind: DocKind, Rev: 1, SHA256: "sha256:x", Country: fileOf(body)}); err == nil {
		t.Fatal("unreachable controller accepted")
	}

	if st.Current() != before {
		t.Fatal("snapshot replaced by a copy that never arrived")
	}

	if got := names(t, dir); len(got) != 0 {
		t.Fatalf("leftovers: %v", got)
	}
}

func TestRestoreKeepsNewestIntactCopy(t *testing.T) {
	dir := t.TempDir()
	older, newer := countryDB("DE"), countryDB("FR")

	write := func(name string, body []byte, age time.Duration) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, body, 0o644); err != nil {
			t.Fatal(err)
		}

		at := time.Now().Add(-age)
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}

		return path
	}

	hexPart := func(b []byte) string { return strings.TrimPrefix(shaOf(b), "sha256:") }

	oldPath := write(fileName(load.KindCountry, hexPart(older)), older, time.Hour)
	newPath := write(fileName(load.KindCountry, hexPart(newer)), newer, time.Minute)
	badPath := write(fileName(load.KindASN, strings.Repeat("0", 64)), []byte("corrupted"), time.Minute)
	partPath := write("country-123.part", []byte("half"), time.Minute)
	foreign := write("README", []byte("not ours"), time.Minute)

	kept, err := Restore(dir, quiet)
	if err != nil {
		t.Fatal(err)
	}

	if got := kept[load.KindCountry]; got.Path != newPath || got.SHA256 != shaOf(newer) {
		t.Fatalf("country: %+v", got)
	}

	if _, ok := kept[load.KindASN]; ok {
		t.Fatal("corrupted asn copy restored")
	}

	for _, gone := range []string{oldPath, badPath, partPath} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s kept: %v", filepath.Base(gone), err)
		}
	}

	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("foreign file removed: %v", err)
	}
}

func TestParseDoc(t *testing.T) {
	sha := shaOf([]byte("x"))
	good := `{"v":1,"kind":"geo","rev":2,"sha256":"sha256:doc",` +
		`"country":{"sha256":"` + sha + `","size":10,"type":"GeoLite2-Country","build":1}}`

	doc, err := ParseDoc([]byte(good))
	if err != nil {
		t.Fatal(err)
	}

	if doc.Rev != 2 || doc.Country == nil || doc.ASN != nil {
		t.Fatalf("parsed: %+v", doc)
	}

	for name, raw := range map[string]string{
		"kind":   `{"v":1,"kind":"haproxy-conf","rev":1,"sha256":"sha256:doc"}`,
		"rev":    `{"v":1,"kind":"geo","rev":0,"sha256":"sha256:doc"}`,
		"sha256": `{"v":1,"kind":"geo","rev":1,"sha256":"sha256:doc","asn":{"sha256":"md5:1","size":1,"type":"x","build":1}}`,
		"size":   `{"v":1,"kind":"geo","rev":1,"sha256":"sha256:doc","asn":{"sha256":"` + sha + `","size":0,"type":"x","build":1}}`,
		"json":   `{`,
	} {
		if _, err := ParseDoc([]byte(raw)); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
}

// entry -- событие KV в том виде, в каком его отдаёт watch.
type entry struct {
	value []byte
	op    jetstream.KeyValueOp
}

func (e entry) Bucket() string                  { return Bucket }
func (e entry) Key() string                     { return Key }
func (e entry) Value() []byte                   { return e.value }
func (e entry) Revision() uint64                { return 1 }
func (e entry) Created() time.Time              { return time.Time{} }
func (e entry) Delta() uint64                   { return 0 }
func (e entry) Operation() jetstream.KeyValueOp { return e.op }

func TestFollowRetriesUntilCopyArrives(t *testing.T) {
	st := testStore(t)
	body := countryDB("DE")
	ctrl := &controller{files: map[string][]byte{}}
	s := New(t.TempDir(), ctrl.serve(t), st, quiet)

	raw, err := json.Marshal(Doc{V: 1, Kind: DocKind, Rev: 3, SHA256: "sha256:doc", Country: fileOf(body)})
	if err != nil {
		t.Fatal(err)
	}

	updates := make(chan jetstream.KeyValueEntry, 2)
	updates <- entry{value: raw, op: jetstream.KeyValuePut}
	updates <- nil

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		s.follow(ctx, updates, backoff{first: 10 * time.Millisecond, max: 40 * time.Millisecond})
		close(done)
	}()

	defer func() {
		cancel()
		<-done
	}()

	/* Файла у контроллера ещё нет: 404, кодер на прежнем и повторяет. */
	waitConf(t, s, 3, ApplyFailed)

	if code := codeAt(st.Current(), "8.8.8.8"); code != "us" {
		t.Fatalf("before the copy: %q", code)
	}

	ctrl.set("country", body)
	waitConf(t, s, 3, ApplyOK)

	if code := codeAt(st.Current(), "8.8.8.8"); code != "de" {
		t.Fatalf("after retry: %q", code)
	}
}

func waitConf(t *testing.T, s *Syncer, rev int, apply string) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		if r, _, a := s.Conf(); r == rev && a == apply {
			return
		}

		time.Sleep(2 * time.Millisecond)
	}

	r, sha, a := s.Conf()
	t.Fatalf("conf rev=%d sha=%s apply=%s, want rev=%d apply=%s", r, sha, a, rev, apply)
}
