/*
 * Один разбор входа: адрес или CIDR, на выходе списки country и asn.
 *
 * Точный адрес и диапазон отвечают по-разному, и это намеренно.
 *
 * Адрес -- вопрос «кто это»: все анонсы, накрывающие адрес, от самого узкого
 * к самому широкому; первым -- эффективный, тот, чей кусок под адресом
 * остался после вычета более узких, и у него же эффективный диапазон. Что из
 * этого писать в набор при бане -- решает профиль; кодер отдаёт факты.
 * Панель читает `asns[0]` как систему адреса, и порядок ей это обещает.
 *
 * Диапазон -- вопрос «кто здесь есть»: каждая система один раз, как и
 * раньше -- для таблицы карточек и обхода каталога.
 *
 * `expand_asn` дописывает каждой системе её состав целиком: второй ярус бана
 * -- «система целиком» -- это все её анонсы, и разворачивать их должен тот, у
 * кого они есть.
 */

package lookup

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"

	"github.com/exemt/placitum-geo/internal/store"
	"github.com/exemt/placitum-geo/internal/table"
)

type Country struct {
	Code string `json:"code"`
	Name string `json:"name,omitempty"`
}

// Range -- эффективный кусок под адресом, включительно с обоих концов.
type Range struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type ASN struct {
	ASN  uint32 `json:"asn"`
	Name string `json:"name,omitempty"`
	// Prefix -- анонс, накрывший запрошенный адрес. Нужен корзинам капчи:
	// субъект «сеть» -- анонсированная подсеть, а не нарезка маской.
	Prefix string `json:"prefix,omitempty"`
	// Effective -- этот анонс побеждает под адресом: его кусок остался после
	// вычета более узких. Ровно у одной строки ответа на адрес.
	Effective bool `json:"effective,omitempty"`
	// Range -- тот самый кусок. Только у эффективной строки.
	Range *Range `json:"range,omitempty"`
	// Prefixes -- состав системы целиком; только по просьбе (expand_asn).
	Prefixes []string `json:"prefixes,omitempty"`
}

type Result struct {
	// Gen -- поколение снимка кодера: сменилось -- кэши клиентов устарели.
	Gen       uint64    `json:"gen"`
	Countries []Country `json:"countries"`
	ASNs      []ASN     `json:"asns"`
}

// Options -- чего просит клиент сверх адреса.
type Options struct {
	// ExpandASN -- дописать каждой системе её состав.
	ExpandASN bool
}

func Parse(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, fmt.Errorf("empty addr")
	}

	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), nil
	}

	if a, err := netip.ParseAddr(s); err == nil {
		a = a.Unmap()

		return netip.PrefixFrom(a, a.BitLen()), nil
	}

	return netip.Prefix{}, fmt.Errorf("not an ip or cidr: %q", s)
}

// BatchItem — один адрес пачки: тот же Result, но с эхом входа и текстом
// ошибки вместо статуса. HTTP-пачка не разваливается на первом плохом
// адресе — падает только сам этот элемент, соседи размечаются как обычно.
type BatchItem struct {
	Addr      string    `json:"addr"`
	Countries []Country `json:"countries"`
	ASNs      []ASN     `json:"asns"`
	Error     string    `json:"error,omitempty"`
}

// DoMany — Do на список адресов, порядок ответа совпадает с порядком входа.
// Отдельная функция, а не цикл в httpapi: разбор пачки — та же логика, что
// один Lookup, и стоит проверяться тестами без http-обвязки.
func DoMany(snap *store.Snapshot, addrs []string) []BatchItem {
	out := make([]BatchItem, len(addrs))

	for i, addr := range addrs {
		res, err := Do(snap, addr)
		item := BatchItem{Addr: addr, Countries: res.Countries, ASNs: res.ASNs}

		if err != nil {
			item.Countries = []Country{}
			item.ASNs = []ASN{}
			item.Error = err.Error()
		}

		out[i] = item
	}

	return out
}

// Do -- ответ без состава: корзинам и таблицам он не нужен.
func Do(snap *store.Snapshot, addr string) (Result, error) {
	return DoWith(snap, addr, Options{})
}

func DoWith(snap *store.Snapshot, addr string, opt Options) (Result, error) {
	p, err := Parse(addr)
	if err != nil {
		return Result{}, err
	}

	out := Result{
		Countries: []Country{},
		ASNs:      []ASN{},
	}

	if snap == nil {
		return out, nil
	}

	out.Gen = snap.Gen

	for _, h := range snap.Country.Covering(p) {
		out.Countries = append(out.Countries, Country{Code: h.Code, Name: h.Name})
	}

	if p.Bits() == p.Addr().BitLen() {
		out.ASNs = coveringAddr(snap, p.Addr())
	} else {
		out.ASNs = withinRange(snap, p)
	}

	if opt.ExpandASN {
		for i := range out.ASNs {
			out.ASNs[i].Prefixes = composition(snap.ASNIndex, out.ASNs[i].ASN)
		}
	}

	return out, nil
}

/*
 * Все анонсы под адресом, эффективный первым. Эффективный узнаётся у
 * сплющенной таблицы -- она и есть арбитр перекрытий; индекс лишь достаёт
 * остальных, которых она забыла.
 */
func coveringAddr(snap *store.Snapshot, addr netip.Addr) []ASN {
	hit, hasHit := snap.ASN.Lookup(addr)
	anns := snap.ASNIndex.Covering(addr)

	rows := make([]ASN, 0, len(anns)+1)
	marked := false

	for _, a := range anns {
		n, ok := table.ParseASN(a.Code)
		if !ok {
			continue
		}

		row := ASN{ASN: n, Name: a.Name, Prefix: a.Prefix.String()}

		if hasHit && !marked && a.Code == hit.Code && a.Prefix == hit.Prefix {
			row.Effective = true
			row.Range = rangeOf(hit)
			marked = true
		}

		rows = append(rows, row)
	}

	/*
	 * Индекса нет или он не знает победителя (старые данные без префикса
	 * анонса): строка из сплющенной таблицы, как отвечали всегда.
	 */
	if hasHit && !marked {
		if n, ok := table.ParseASN(hit.Code); ok {
			row := ASN{ASN: n, Name: hit.Name, Effective: true, Range: rangeOf(hit)}

			if hit.Prefix.IsValid() {
				row.Prefix = hit.Prefix.String()
			}

			rows = append([]ASN{row}, rows...)
		}
	}

	// Эффективный -- первым, остальные как шли: от узкого к широкому.
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Effective && !rows[j].Effective })

	return rows
}

// Каждая система один раз, по номеру -- ответ на диапазон, как и прежде.
func withinRange(snap *store.Snapshot, p netip.Prefix) []ASN {
	rows := []ASN{}

	for _, h := range snap.ASN.Covering(p) {
		n, ok := table.ParseASN(h.Code)
		if !ok {
			continue
		}

		row := ASN{ASN: n, Name: h.Name}

		if h.Prefix.IsValid() {
			row.Prefix = h.Prefix.String()
		}

		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool { return rows[i].ASN < rows[j].ASN })

	return rows
}

func rangeOf(h table.Hit) *Range {
	if !h.Start.IsValid() || !h.End.IsValid() {
		return nil
	}

	return &Range{Start: h.Start.String(), End: h.End.String()}
}

func composition(x *table.Index, asn uint32) []string {
	ps := x.Prefixes(fmt.Sprintf("%d", asn))
	if len(ps) == 0 {
		return nil
	}

	out := make([]string, len(ps))

	for i, p := range ps {
		out[i] = p.String()
	}

	return out
}
