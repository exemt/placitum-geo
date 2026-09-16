package fetch

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/exemt/placitum-geo/internal/load"
	"github.com/exemt/placitum-geo/internal/store"
)

const (
	Bucket  = "WAF_DESIRED"
	Key     = "policy/geo"
	DocKind = "geo"

	ApplyOK       = "ok"
	ApplyFetching = "fetching"
	ApplyFailed   = "fetch_failed"

	MaxSize = 256 << 20

	fetchTimeout = 2 * time.Minute
	watchRetry   = 5 * time.Second
)

var retryPace = backoff{first: time.Second, max: 30 * time.Second}

type backoff struct {
	first time.Duration
	max   time.Duration
}

type File struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Type   string `json:"type"`
	Build  int64  `json:"build"`
}

type Doc struct {
	V       int    `json:"v"`
	Kind    string `json:"kind"`
	Rev     int    `json:"rev"`
	SHA256  string `json:"sha256"`
	Country *File  `json:"country,omitempty"`
	ASN     *File  `json:"asn,omitempty"`
}

func ParseDoc(raw []byte) (*Doc, error) {
	var doc Doc

	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("geo doc: %w", err)
	}

	if doc.V != 1 || doc.Kind != DocKind {
		return nil, fmt.Errorf("geo doc: unsupported v=%d kind=%q", doc.V, doc.Kind)
	}

	if doc.Rev < 1 {
		return nil, errors.New("geo doc: rev must be positive")
	}

	for _, f := range []*File{doc.Country, doc.ASN} {
		if f == nil {
			continue
		}

		if _, ok := hexOf(f.SHA256); !ok {
			return nil, fmt.Errorf("geo doc: bad sha256 %q", f.SHA256)
		}

		if f.Size <= 0 || f.Size > MaxSize {
			return nil, fmt.Errorf("geo doc: bad size %d", f.Size)
		}
	}

	return &doc, nil
}

func (d *Doc) file(kind load.Kind) *File {
	if kind == load.KindASN {
		return d.ASN
	}

	return d.Country
}

var kinds = []load.Kind{load.KindCountry, load.KindASN}

func hexOf(sha string) (string, bool) {
	part, ok := strings.CutPrefix(sha, "sha256:")
	if !ok || len(part) != sha256.Size*2 {
		return "", false
	}

	if _, err := hex.DecodeString(part); err != nil {
		return "", false
	}

	return part, true
}

func fileName(kind load.Kind, hexPart string) string {
	return kind.String() + "-" + hexPart + ".mmdb"
}

func parseName(name string) (load.Kind, bool) {
	base, ok := strings.CutSuffix(name, ".mmdb")
	if !ok {
		return 0, false
	}

	for _, kind := range kinds {
		if part, ok := strings.CutPrefix(base, kind.String()+"-"); ok {
			if _, ok := hexOf("sha256:" + part); ok {
				return kind, true
			}
		}
	}

	return 0, false
}

func Restore(dir string, log *slog.Logger) (map[load.Kind]store.Source, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	type candidate struct {
		path string
		mod  time.Time
	}

	found := map[load.Kind][]candidate{}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		path := filepath.Join(dir, e.Name())

		kind, ok := parseName(e.Name())
		if !ok {
			if strings.HasSuffix(e.Name(), ".part") {
				_ = os.Remove(path)
			}

			continue
		}

		info, err := e.Info()
		if err != nil {
			continue
		}

		found[kind] = append(found[kind], candidate{path: path, mod: info.ModTime()})
	}

	out := map[load.Kind]store.Source{}

	for kind, list := range found {
		sort.Slice(list, func(i, j int) bool { return list[i].mod.After(list[j].mod) })

		for _, c := range list {
			if _, ok := out[kind]; ok {
				_ = os.Remove(c.path)
				continue
			}

			want := "sha256:" + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(c.path), kind.String()+"-"), ".mmdb")

			got, err := hashFile(c.path)
			if err != nil || got != want {
				log.Warn("geo copy dropped", "path", c.path, "reason", "hash does not match the name")
				_ = os.Remove(c.path)

				continue
			}

			out[kind] = store.Source{Path: c.path, SHA256: want}
		}
	}

	return out, nil
}

type Syncer struct {
	dir    string
	base   string
	client *http.Client
	store  *store.Store
	log    *slog.Logger

	mu    sync.RWMutex
	rev   int
	sha   string
	apply string
}

func New(dir, controllerURL string, st *store.Store, log *slog.Logger) *Syncer {
	return &Syncer{
		dir:    dir,
		base:   strings.TrimRight(controllerURL, "/"),
		client: &http.Client{Timeout: fetchTimeout},
		store:  st,
		log:    log,
	}
}

func (s *Syncer) Conf() (rev int, sha string, apply string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.rev, s.sha, s.apply
}

func (s *Syncer) set(rev int, sha, apply string) {
	s.mu.Lock()
	s.rev, s.sha, s.apply = rev, sha, apply
	s.mu.Unlock()
}

func (s *Syncer) Apply(ctx context.Context, doc *Doc) error {
	for _, kind := range kinds {
		want := doc.file(kind)
		if want == nil || s.store.Current().Source(kind).SHA256 == want.SHA256 {
			continue
		}

		path, err := s.download(ctx, kind, want)
		if err != nil {
			return fmt.Errorf("%s: %w", kind, err)
		}

		prev, err := s.store.Use(kind, store.Source{Path: path, SHA256: want.SHA256})
		if err != nil {
			_ = os.Remove(path)
			return fmt.Errorf("%s: %w", kind, err)
		}

		if prev.SHA256 != "" && prev.Path != path {
			if err := os.Remove(prev.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				s.log.Warn("geo copy not removed", "path", prev.Path, "error", err.Error())
			}
		}

		s.log.Info("geo copy applied",
			"kind", kind.String(),
			"rev", doc.Rev,
			"sha256", want.SHA256,
			"type", want.Type,
			"build", want.Build,
			"gen", s.store.Current().Gen,
		)
	}

	return nil
}

func (s *Syncer) download(ctx context.Context, kind load.Kind, want *File) (string, error) {
	hexPart, ok := hexOf(want.SHA256)
	if !ok {
		return "", fmt.Errorf("bad sha256 %q", want.SHA256)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+"/api/geo/files/"+kind.String(), nil)
	if err != nil {
		return "", err
	}

	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("controller answered %d", res.StatusCode)
	}

	tmp, err := os.CreateTemp(s.dir, kind.String()+"-*.part")
	if err != nil {
		return "", err
	}

	done := false
	defer func() {
		if !done {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()

	h := sha256.New()

	n, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(res.Body, MaxSize+1))
	if err != nil {
		return "", err
	}

	if n > MaxSize {
		return "", fmt.Errorf("copy is larger than %d bytes", MaxSize)
	}

	if n != want.Size {
		return "", fmt.Errorf("got %d bytes, want %d", n, want.Size)
	}

	if got := hex.EncodeToString(h.Sum(nil)); got != hexPart {
		return "", fmt.Errorf("got sha256:%s, want %s", got, want.SHA256)
	}

	if err := tmp.Sync(); err != nil {
		return "", err
	}

	if err := tmp.Close(); err != nil {
		return "", err
	}

	path := filepath.Join(s.dir, fileName(kind, hexPart))

	if err := os.Rename(tmp.Name(), path); err != nil {
		_ = os.Remove(tmp.Name())
		done = true

		return "", err
	}

	done = true

	return path, nil
}

func (s *Syncer) Watch(ctx context.Context, nc *nats.Conn) {
	said := false

	for {
		err := s.watchOnce(ctx, nc)
		if ctx.Err() != nil {
			return
		}

		if !said && err != nil {
			s.log.Warn("geo doc watch", "key", Key, "error", err.Error())
			said = true
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(watchRetry):
		}
	}
}

func (s *Syncer) watchOnce(ctx context.Context, nc *nats.Conn) error {
	js, err := jetstream.New(nc)
	if err != nil {
		return err
	}

	kv, err := js.KeyValue(ctx, Bucket)
	if err != nil {
		return err
	}

	watcher, err := kv.Watch(ctx, Key)
	if err != nil {
		return err
	}
	defer watcher.Stop()

	s.follow(ctx, watcher.Updates(), retryPace)

	if ctx.Err() != nil {
		return nil
	}

	return errors.New("watch closed")
}

func (s *Syncer) follow(ctx context.Context, updates <-chan jetstream.KeyValueEntry, pace backoff) {
	var (
		pending *Doc
		retry   <-chan time.Time
		delay   = pace.first
	)

	try := func(doc *Doc) {
		pending, retry = nil, nil
		s.set(doc.Rev, doc.SHA256, ApplyFetching)

		err := s.Apply(ctx, doc)
		if err == nil {
			s.set(doc.Rev, doc.SHA256, ApplyOK)
			delay = pace.first

			return
		}

		if ctx.Err() != nil {
			return
		}

		s.set(doc.Rev, doc.SHA256, ApplyFailed)
		s.log.Warn("geo doc apply failed",
			"rev", doc.Rev,
			"sha256", doc.SHA256,
			"error", err.Error(),
			"retry_in", delay.String(),
		)

		pending, retry = doc, time.After(delay)
		delay = min(2*delay, pace.max)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-retry:
			try(pending)
		case entry, ok := <-updates:
			if !ok {
				return
			}

			if entry == nil {
				continue
			}

			switch entry.Operation() {
			case jetstream.KeyValueDelete, jetstream.KeyValuePurge:
				continue
			}

			doc, err := ParseDoc(entry.Value())
			if err != nil {
				s.log.Warn("geo doc rejected", "key", Key, "error", err.Error())
				continue
			}

			if rev, sha, apply := s.Conf(); rev == doc.Rev && sha == doc.SHA256 && apply == ApplyOK {
				continue
			}

			delay = pace.first
			try(doc)
		}
	}
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	h := sha256.New()

	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
