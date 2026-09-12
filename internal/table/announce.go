/*
 * Исходные анонсы -- то, чего сплющенная таблица не хранит.
 *
 * Table режет перекрытия: в каждой точке побеждает самый узкий анонс, и
 * остальные накрывающие теряются ещё при загрузке. Для корзин это верно --
 * субъект «сеть» один. Для бана нужны все: адрес накрыт и /24 одной системы,
 * и, случается, /9 другой, и что из этого писать в набор -- решает профиль,
 * а не кодер. Кодер отдаёт факты.
 *
 * Индекс -- карта «префикс -> анонсы» и карта «код -> префиксы». Стаб по
 * адресу -- проверка его 33 (v4) или 129 (v6) префиксов по карте, это быстрее
 * любого дерева и не зависит от размера таблицы. Состав системы -- второй
 * ярус бана, «AS целиком».
 */

package table

import (
	"net/netip"
	"sort"
)

// Announce -- анонс как загружен: префикс, код (для ASN -- номер строкой), имя.
type Announce struct {
	Prefix netip.Prefix
	Code   string
	Name   string
}

type Index struct {
	byPrefix map[netip.Prefix][]Announce
	byCode   map[string][]netip.Prefix
	names    map[string]string
}

// BuildIndex собирает индекс из тех же записей, что и Build. Пара
// «префикс + код» считается один раз: одна и та же строка в двух файлах --
// не два анонса.
func BuildIndex(in []Entry) *Index {
	x := &Index{
		byPrefix: map[netip.Prefix][]Announce{},
		byCode:   map[string][]netip.Prefix{},
		names:    map[string]string{},
	}

	seen := map[string]struct{}{}

	for _, e := range in {
		p, ok := canonPrefix(e.Prefix)
		if !ok || e.Code == "" {
			continue
		}

		key := p.String() + "|" + e.Code
		if _, dup := seen[key]; dup {
			continue
		}

		seen[key] = struct{}{}

		x.byPrefix[p] = append(x.byPrefix[p], Announce{Prefix: p, Code: e.Code, Name: e.Name})
		x.byCode[e.Code] = append(x.byCode[e.Code], p)

		if x.names[e.Code] == "" && e.Name != "" {
			x.names[e.Code] = e.Name
		}
	}

	// MOAS: одна сеть, несколько систем -- по коду, чтобы ответ был стабилен.
	for _, anns := range x.byPrefix {
		sort.Slice(anns, func(i, j int) bool { return anns[i].Code < anns[j].Code })
	}

	// Состав -- по адресу, при равном адресе шире раньше: так читается глазами.
	for _, ps := range x.byCode {
		sort.Slice(ps, func(i, j int) bool {
			if c := ps[i].Addr().Compare(ps[j].Addr()); c != 0 {
				return c < 0
			}

			return ps[i].Bits() < ps[j].Bits()
		})
	}

	return x
}

/*
 * Covering -- все анонсы, накрывающие адрес, от самого узкого к самому
 * широкому; внутри одной сети (MOAS) -- по коду. Самый узкий -- тот, что
 * побеждает в сплющенной таблице, и он идёт первым.
 */
func (x *Index) Covering(addr netip.Addr) []Announce {
	if x == nil {
		return nil
	}

	addr = addr.Unmap()
	if !addr.Is4() && !addr.Is6() {
		return nil
	}

	var out []Announce

	for bits := addr.BitLen(); bits >= 0; bits-- {
		if anns, ok := x.byPrefix[netip.PrefixFrom(addr, bits).Masked()]; ok {
			out = append(out, anns...)
		}
	}

	return out
}

// Prefixes -- состав системы: все её анонсы, отсортированные. Срез общий и
// только для чтения: состав крупной системы -- десятки тысяч префиксов, и
// копировать его на каждый вопрос незачем.
func (x *Index) Prefixes(code string) []netip.Prefix {
	if x == nil {
		return nil
	}

	return x.byCode[code]
}

func (x *Index) Name(code string) string {
	if x == nil {
		return ""
	}

	return x.names[code]
}

// Codes -- сколько систем в индексе; для статистики.
func (x *Index) Codes() int {
	if x == nil {
		return 0
	}

	return len(x.byCode)
}

/*
 * Один вид префикса: v4, вложенный в v6 (::ffff:8.8.8.0/120), становится
 * обычным v4 /24 -- иначе одна и та же сеть жила бы в карте дважды и
 * стаб по v4-адресу её бы не нашёл.
 */
func canonPrefix(p netip.Prefix) (netip.Prefix, bool) {
	if !p.IsValid() {
		return netip.Prefix{}, false
	}

	addr := p.Addr()
	bits := p.Bits()

	if addr.Is4In6() {
		addr = addr.Unmap()
		bits -= 96

		if bits < 0 {
			return netip.Prefix{}, false
		}
	}

	if !addr.Is4() && !addr.Is6() {
		return netip.Prefix{}, false
	}

	return netip.PrefixFrom(addr, bits).Masked(), true
}
