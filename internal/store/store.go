/*
 * Снапшот country и asn в памяти.
 *
 * Current() отдаёт указатель без блокировки. Ошибка чтения оставляет
 * действующий набор. Наблюдение -- опрос отпечатка, не inotify.
 */

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
	"sync/atomic"
	"time"

	"github.com/exemt/placitum-geo/internal/load"
	"github.com/exemt/placitum-geo/internal/table"
)

type Paths struct {
	Country string
	ASN     string
}

type Snapshot struct {
	Gen     uint64
	Country *table.Table
	ASN     *table.Table
	/*
	 * ASNIndex -- исходные анонсы систем, не сплющенные: все накрывающие
	 * адрес и состав каждой системы. Строится из тех же записей, что и ASN,
	 * атомарно с ним: снимок либо целиком новый, либо целиком прежний.
	 */
	ASNIndex    *table.Index
	Fingerprint string
	LoadedAt    time.Time
	Skipped     int
}

type Stats struct {
	Gen         uint64 `json:"gen"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Countries   int    `json:"countries"`
	ASNs        int    `json:"asns"`
	Skipped     int    `json:"skipped"`
}

func (s *Snapshot) Stats() Stats {
	if s == nil {
		return Stats{}
	}

	return Stats{
		Gen:         s.Gen,
		Fingerprint: s.Fingerprint,
		Countries:   s.Country.Len(),
		ASNs:        s.ASN.Len(),
		Skipped:     s.Skipped,
	}
}

type Store struct {
	paths Paths
	log   *slog.Logger
	cur   atomic.Pointer[Snapshot]
	gen   atomic.Uint64
}

func Load(paths Paths, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}

	s := &Store{paths: paths, log: log}

	snap, err := s.build()
	if err != nil {
		return nil, err
	}

	s.cur.Store(snap)

	return s, nil
}

func (s *Store) Current() *Snapshot {
	return s.cur.Load()
}

func (s *Store) Reload() (changed bool, err error) {
	/*
	 * Отпечаток -- до сборки. Сборка разбирает весь каталог (полный каталог
	 * ASN -- 32 МБ и 660 тысяч префиксов) и собирает таблицы; делать это раз
	 * в секунду, чтобы выбросить снимок с тем же отпечатком, значило держать
	 * кодер на целом ядре без единого запроса -- так и было 12.09.
	 */
	fp, err := fingerprint(s.paths)
	if err != nil {
		return false, err
	}

	if cur := s.cur.Load(); cur != nil && cur.Fingerprint == fp {
		return false, nil
	}

	snap, err := s.build()
	if err != nil {
		return false, err
	}

	if cur := s.cur.Load(); cur != nil && cur.Fingerprint == snap.Fingerprint {
		return false, nil
	}

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

func (s *Store) build() (*Snapshot, error) {
	fp, err := fingerprint(s.paths)
	if err != nil {
		return nil, err
	}

	country, skipC, err := loadOne(s.paths.Country, load.KindCountry)
	if err != nil {
		return nil, fmt.Errorf("country: %w", err)
	}

	asn, skipA, err := loadOne(s.paths.ASN, load.KindASN)
	if err != nil {
		return nil, fmt.Errorf("asn: %w", err)
	}

	return &Snapshot{
		Gen:         s.gen.Add(1),
		Country:     table.Build(country),
		ASN:         table.Build(asn),
		ASNIndex:    table.BuildIndex(asn),
		Fingerprint: fp,
		LoadedAt:    time.Now().UTC(),
		Skipped:     skipC + skipA,
	}, nil
}

func loadOne(path string, kind load.Kind) ([]table.Entry, int, error) {
	if path == "" {
		return nil, 0, nil
	}

	return load.Path(path, kind)
}

func fingerprint(p Paths) (string, error) {
	h := sha256.New()

	var paths []string

	for _, root := range []string{p.Country, p.ASN} {
		if root == "" {
			continue
		}

		st, err := os.Stat(root)
		if err != nil {
			return "", err
		}

		if !st.IsDir() {
			paths = append(paths, root)
			continue
		}

		files, err := listFiles(root)
		if err != nil {
			return "", err
		}

		paths = append(paths, files...)
	}

	sort.Strings(paths)

	for _, path := range paths {
		fi, err := os.Stat(path)
		if err != nil {
			return "", err
		}

		fmt.Fprintf(h, "%s\t%d\t%d\n", filepath.ToSlash(path), fi.Size(), fi.ModTime().UnixNano())
	}

	return hex.EncodeToString(h.Sum(nil)), nil
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
