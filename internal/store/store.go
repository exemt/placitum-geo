package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/exemt/placitum-geo/internal/load"
	"github.com/exemt/placitum-geo/internal/table"
)

type Paths struct {
	Country string
	ASN     string
}

type Source struct {
	Path   string
	SHA256 string
}

type Sources struct {
	Country Source
	ASN     Source
}

type Snapshot struct {
	Gen         uint64
	Country     *table.Table
	ASN         *table.Table
	ASNIndex    *table.Index
	Fingerprint string
	LoadedAt    time.Time
	Skipped     int

	country layer
	asn     layer
}

type layer struct {
	src     Source
	fp      string
	skipped int
}

type Stats struct {
	Gen           uint64 `json:"gen"`
	Fingerprint   string `json:"fingerprint,omitempty"`
	Countries     int    `json:"countries"`
	ASNs          int    `json:"asns"`
	Skipped       int    `json:"skipped"`
	CountrySHA256 string `json:"country_sha256,omitempty"`
	ASNSHA256     string `json:"asn_sha256,omitempty"`
}

func (s *Snapshot) Stats() Stats {
	if s == nil {
		return Stats{}
	}

	return Stats{
		Gen:           s.Gen,
		Fingerprint:   s.Fingerprint,
		Countries:     s.Country.Len(),
		ASNs:          s.ASN.Len(),
		Skipped:       s.Skipped,
		CountrySHA256: s.country.src.SHA256,
		ASNSHA256:     s.asn.src.SHA256,
	}
}

func (s *Snapshot) Source(kind load.Kind) Source {
	if s == nil {
		return Source{}
	}

	if kind == load.KindASN {
		return s.asn.src
	}

	return s.country.src
}

type part struct {
	layer
	kind  load.Kind
	table *table.Table
	index *table.Index
}

func (s *Snapshot) partOf(kind load.Kind) part {
	if kind == load.KindASN {
		return part{layer: s.asn, kind: kind, table: s.ASN, index: s.ASNIndex}
	}

	return part{layer: s.country, kind: kind, table: s.Country}
}

type Store struct {
	log *slog.Logger
	cur atomic.Pointer[Snapshot]
	gen atomic.Uint64

	mu sync.Mutex
}

func Load(paths Paths, log *slog.Logger) (*Store, error) {
	return LoadSources(Sources{
		Country: Source{Path: paths.Country},
		ASN:     Source{Path: paths.ASN},
	}, log)
}

func LoadSources(src Sources, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}

	s := &Store{log: log}

	country, err := build(load.KindCountry, src.Country)
	if err != nil {
		return nil, fmt.Errorf("country: %w", err)
	}

	asn, err := build(load.KindASN, src.ASN)
	if err != nil {
		return nil, fmt.Errorf("asn: %w", err)
	}

	s.cur.Store(s.assemble(country, asn))

	return s, nil
}

func (s *Store) Current() *Snapshot {
	return s.cur.Load()
}

func (s *Store) Reload() (changed bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.cur.Load()
	country, asn := cur.partOf(load.KindCountry), cur.partOf(load.KindASN)

	for _, p := range []*part{&country, &asn} {
		fp, err := fingerprint(p.src.Path)
		if err != nil {
			return false, fmt.Errorf("%s: %w", p.kind, err)
		}

		if fp == p.fp {
			continue
		}

		next, err := build(p.kind, p.src)
		if err != nil {
			return false, fmt.Errorf("%s: %w", p.kind, err)
		}

		*p = next
		changed = true
	}

	if !changed {
		return false, nil
	}

	snap := s.assemble(country, asn)
	s.cur.Store(snap)
	st := snap.Stats()
	s.log.Info("data reloaded",
		"gen", st.Gen,
		"countries", st.Countries,
		"asns", st.ASNs,
		"skipped", st.Skipped,
	)

	return true, nil
}

func (s *Store) Use(kind load.Kind, src Source) (Source, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cur := s.cur.Load()
	country, asn := cur.partOf(load.KindCountry), cur.partOf(load.KindASN)
	prev := cur.Source(kind)

	next, err := build(kind, src)
	if err != nil {
		return Source{}, err
	}

	if kind == load.KindASN {
		asn = next
	} else {
		country = next
	}

	snap := s.assemble(country, asn)
	s.cur.Store(snap)
	st := snap.Stats()
	s.log.Info("data switched",
		"kind", kind.String(),
		"sha256", src.SHA256,
		"gen", st.Gen,
		"countries", st.Countries,
		"asns", st.ASNs,
		"skipped", st.Skipped,
	)

	return prev, nil
}

func (s *Store) Watch(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Second
	}

	tick := time.NewTicker(every)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			if _, err := s.Reload(); err != nil {
				s.log.Warn("reload failed, keeping current snapshot", "error", err.Error())
			}
		}
	}
}

func build(kind load.Kind, src Source) (part, error) {
	fp, err := fingerprint(src.Path)
	if err != nil {
		return part{}, err
	}

	entries, skipped, err := loadOne(src.Path, kind)
	if err != nil {
		return part{}, err
	}

	p := part{
		layer: layer{src: src, fp: fp, skipped: skipped},
		kind:  kind,
		table: table.Build(entries),
	}

	if kind == load.KindASN {
		p.index = table.BuildIndex(entries)
	}

	return p, nil
}

func (s *Store) assemble(country, asn part) *Snapshot {
	return &Snapshot{
		Gen:         s.gen.Add(1),
		Country:     country.table,
		ASN:         asn.table,
		ASNIndex:    asn.index,
		Fingerprint: combine(country.fp, asn.fp),
		LoadedAt:    time.Now().UTC(),
		Skipped:     country.skipped + asn.skipped,
		country:     country.layer,
		asn:         asn.layer,
	}
}

func loadOne(path string, kind load.Kind) ([]table.Entry, int, error) {
	if path == "" {
		return nil, 0, nil
	}

	return load.Path(path, kind)
}

func fingerprint(root string) (string, error) {
	if root == "" {
		return "", nil
	}

	st, err := os.Stat(root)
	if err != nil {
		return "", err
	}

	paths := []string{root}

	if st.IsDir() {
		if paths, err = listFiles(root); err != nil {
			return "", err
		}
	}

	h := sha256.New()

	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}

		fmt.Fprintf(h, "%s\t%d\t%d\n", filepath.ToSlash(path), fi.Size(), fi.ModTime().UnixNano())
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func combine(country, asn string) string {
	h := sha256.New()
	fmt.Fprintf(h, "country\t%s\nasn\t%s\n", country, asn)

	return hex.EncodeToString(h.Sum(nil))
}

func listFiles(root string) ([]string, error) {
	var out []string

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() {
			if strings.HasPrefix(d.Name(), ".") && path != root {
				return fs.SkipDir
			}

			return nil
		}

		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}

		out = append(out, path)

		return nil
	})
	if err != nil {
		return nil, err
	}

	sort.Strings(out)

	return out, nil
}
