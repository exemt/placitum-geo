/*
 * Сборка помеченных префиксов из каталога, TSV или MaxMind DB.
 *
 * Каталог -- как у инспектора адреса: <код>.txt или <код>/ranges.txt,
 * `# name: ...` в файле задаёт подпись. TSV -- выгрузка контроллера:
 * code, type, address, name. MMDB -- GeoLite2 Country / ASN, обход
 * всех сетей один раз при загрузке.
 */

package load

import (
	"bufio"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/oschwald/maxminddb-golang/v2"

	"github.com/exemt/placitum-geo/internal/table"
)

type Kind int

const (
	KindCountry Kind = iota
	KindASN
)

var (
	codeRe = regexp.MustCompile(`^[a-z]{2}$`)
	nameRe = regexp.MustCompile(`(?i)^#\s*name:\s*(.+)$`)
)

type countryRec struct {
	Country struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"country"`
	RegisteredCountry struct {
		ISOCode string            `maxminddb:"iso_code"`
		Names   map[string]string `maxminddb:"names"`
	} `maxminddb:"registered_country"`
}

type asnRec struct {
	Number uint   `maxminddb:"autonomous_system_number"`
	Org    string `maxminddb:"autonomous_system_organization"`
}

func Path(path string, kind Kind) ([]table.Entry, int, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, 0, err
	}

	if st.IsDir() {
		return Dir(path, kind)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".mmdb":
		return MMDB(path, kind)
	case ".tsv":
		return TSVFile(path, kind)
	}

	return File(path, kind)
}

func Dir(root string, kind Kind) ([]table.Entry, int, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, 0, err
	}

	var (
		out     []table.Entry
		skipped int
	)

	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}

		path := filepath.Join(root, e.Name())

		if e.IsDir() {
			code, name, ok := labelOf(e.Name(), kind)
			if !ok {
				return nil, 0, fmt.Errorf("%s: invalid %s key", e.Name(), kindName(kind))
			}

			files, err := listFiles(path)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: %w", e.Name(), err)
			}

			for _, file := range files {
				es, skip, n, err := readPrefixes(file, code, name)
				if err != nil {
					return nil, 0, fmt.Errorf("%s: %w", filepath.Base(file), err)
				}

				if n != "" {
					name = n
					for i := range es {
						es[i].Name = n
					}
				}

				out = append(out, es...)
				skipped += skip
			}

			continue
		}

		if strings.EqualFold(filepath.Ext(e.Name()), ".tsv") {
			es, skip, err := TSVFile(path, kind)
			if err != nil {
				return nil, 0, fmt.Errorf("%s: %w", e.Name(), err)
			}

			out = append(out, es...)
			skipped += skip
			continue
		}

		es, skip, err := File(path, kind)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", e.Name(), err)
		}

		out = append(out, es...)
		skipped += skip
	}

	return out, skipped, nil
}

func File(path string, kind Kind) ([]table.Entry, int, error) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	code, name, ok := labelOf(base, kind)
	if !ok {
		return nil, 0, fmt.Errorf("invalid %s key %q", kindName(kind), base)
	}

	es, skip, n, err := readPrefixes(path, code, name)
	if err != nil {
		return nil, 0, err
	}

	if n != "" {
		for i := range es {
			es[i].Name = n
		}
	}

	return es, skip, nil
}

func TSVFile(path string, kind Kind) ([]table.Entry, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}

	defer f.Close()

	return TSV(f, kind)
}

func TSV(r io.Reader, kind Kind) ([]table.Entry, int, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		out     []table.Entry
		skipped int
	)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, "\t")
		if len(fields) < 3 {
			skipped++
			continue
		}

		code, name, ok := labelOf(fields[0], kind)
		if !ok {
			skipped++
			continue
		}

		if len(fields) > 3 && fields[3] != "" {
			name = fields[3]
		}

		p, err := parsePrefix(fields[2])
		if err != nil {
			skipped++
			continue
		}

		out = append(out, table.Entry{Prefix: p, Code: code, Name: name})
	}

	return out, skipped, sc.Err()
}

func MMDB(path string, kind Kind) ([]table.Entry, int, error) {
	db, err := maxminddb.Open(path)
	if err != nil {
		return nil, 0, err
	}

	defer db.Close()

	got := kindOf(db.Metadata.DatabaseType)
	if got != kind {
		return nil, 0, fmt.Errorf("%s: database is %s, want %s", path, kindName(got), kindName(kind))
	}

	var (
		out     []table.Entry
		skipped int
	)

	for result := range db.Networks() {
		p := result.Prefix()
		if !p.IsValid() {
			if err := result.Err(); err != nil {
				return nil, 0, err
			}

			skipped++
			continue
		}

		code, name, ok, err := decode(result, kind)
		if err != nil {
			return nil, 0, err
		}

		if !ok {
			skipped++
			continue
		}

		out = append(out, table.Entry{Prefix: p.Masked(), Code: code, Name: name})
	}

	return out, skipped, nil
}

func decode(result maxminddb.Result, kind Kind) (code, name string, ok bool, err error) {
	if kind == KindASN {
		var rec asnRec
		if err := result.Decode(&rec); err != nil {
			return "", "", false, err
		}

		if rec.Number == 0 || rec.Number > uint(^uint32(0)) {
			return "", "", false, nil
		}

		name = rec.Org
		if name == "" {
			name = fmt.Sprintf("AS%d", rec.Number)
		}

		return fmt.Sprintf("%d", rec.Number), name, true, nil
	}

	var rec countryRec
	if err := result.Decode(&rec); err != nil {
		return "", "", false, err
	}

	iso := rec.Country.ISOCode
	names := rec.Country.Names
	if iso == "" {
		iso = rec.RegisteredCountry.ISOCode
		names = rec.RegisteredCountry.Names
	}

	code, name, ok = countryOf(iso, names)

	return code, name, ok, nil
}

func countryOf(iso string, names map[string]string) (string, string, bool) {
	code := strings.ToLower(strings.TrimSpace(iso))
	if !codeRe.MatchString(code) {
		return "", "", false
	}

	name := names["en"]
	if name == "" {
		name = names["ru"]
	}

	if name == "" {
		name = strings.ToUpper(code)
	}

	return code, name, true
}

func kindOf(databaseType string) Kind {
	if strings.Contains(strings.ToLower(databaseType), "asn") {
		return KindASN
	}

	return KindCountry
}

func kindName(k Kind) string {
	if k == KindASN {
		return "asn"
	}

	return "country"
}

// String -- имя вида: country или asn. Им же названы копии выгрузок и путь
// контроллера, с которого они скачиваются.
func (k Kind) String() string {
	return kindName(k)
}

func labelOf(raw string, kind Kind) (code, name string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}

	if kind == KindASN {
		n, ok := table.ParseASN(raw)
		if !ok {
			return "", "", false
		}

		return fmt.Sprintf("%d", n), fmt.Sprintf("AS%d", n), true
	}

	code = strings.ToLower(raw)
	if !codeRe.MatchString(code) {
		return "", "", false
	}

	return code, strings.ToUpper(code), true
}

func readPrefixes(path, code, name string) ([]table.Entry, int, string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, "", err
	}

	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	var (
		out     []table.Entry
		skipped int
	)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if m := nameRe.FindStringSubmatch(line); m != nil {
			name = strings.TrimSpace(m[1])
			continue
		}

		p, err := parsePrefix(line)
		if err != nil {
			if isSkip(err) {
				continue
			}

			skipped++
			continue
		}

		out = append(out, table.Entry{Prefix: p, Code: code, Name: name})
	}

	return out, skipped, name, sc.Err()
}

func parsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" || strings.HasPrefix(s, "#") {
		return netip.Prefix{}, errSkip
	}

	if i := strings.Index(s, "#"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}

	fields := strings.Fields(s)
	if len(fields) == 0 {
		return netip.Prefix{}, errSkip
	}

	tok := fields[0]

	if p, err := netip.ParsePrefix(tok); err == nil {
		return p.Masked(), nil
	}

	if a, err := netip.ParseAddr(tok); err == nil {
		a = a.Unmap()

		return netip.PrefixFrom(a, a.BitLen()), nil
	}

	return netip.Prefix{}, fmt.Errorf("not an ip or cidr: %q", tok)
}

var errSkip = fmt.Errorf("skip")

func isSkip(err error) bool {
	return err == errSkip
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

	return out, nil
}
