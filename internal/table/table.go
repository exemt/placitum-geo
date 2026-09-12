/*
 * Принадлежность адреса или префикса набору помеченных интервалов.
 *
 * Набор неизменяем после сборки: его читают без блокировки, обновление --
 * атомарная подмена указателя снаружи. Структура -- отсортированный массив
 * непересекающихся интервалов и двоичный поиск.
 *
 * Пересекающиеся префиксы режутся по границам; на атомарном куске побеждает
 * самый узкий (longest prefix). Соседние куски с одним кодом сливаются.
 * Для CIDR собираются все коды, чьи интервалы пересекают запрос: два
 * префикса либо вложены, либо не пересекаются, частичного перекрытия нет.
 */

package table

import (
	"net/netip"
	"sort"
	"strconv"
)

type Entry struct {
	Prefix netip.Prefix
	Code   string
	Name   string
}

type Rec struct {
	Start netip.Addr
	End   netip.Addr
	Code  string
	Name  string
	// Prefix -- исходный анонс, из которого вырезан диапазон. Нужен корзинам
	// капчи: субъект «сеть» -- это анонсированный префикс, а не наш кусок.
	Prefix netip.Prefix
}

type Hit struct {
	Code string
	Name string
	// Prefix -- анонс, накрывший запрошенный адрес. Пустой у старых данных.
	Prefix netip.Prefix
	// Start и End -- эффективный кусок под адресом: то, что осталось от анонса
	// после вычета более узких. Заполнены у Lookup; Covering их не знает --
	// у диапазона кусков много.
	Start netip.Addr
	End   netip.Addr
}

type Table struct {
	v4 []Rec
	v6 []Rec
}

func (t *Table) Len() int {
	if t == nil {
		return 0
	}

	return len(t.v4) + len(t.v6)
}

func (t *Table) Lookup(ip netip.Addr) (Hit, bool) {
	if t == nil {
		return Hit{}, false
	}

	ip = ip.Unmap()
	var s []Rec

	if ip.Is4() {
		s = t.v4
	} else if ip.Is6() {
		s = t.v6
	} else {
		return Hit{}, false
	}

	i := sort.Search(len(s), func(i int) bool {
		return s[i].Start.Compare(ip) > 0
	})
	if i == 0 {
		return Hit{}, false
	}

	r := s[i-1]
	if ip.Compare(r.End) > 0 {
		return Hit{}, false
	}

	return Hit{Code: r.Code, Name: r.Name, Prefix: r.Prefix, Start: r.Start, End: r.End}, true
}

func (t *Table) Covering(p netip.Prefix) []Hit {
	if t == nil {
		return []Hit{}
	}

	p = p.Masked()
	start := p.Addr().Unmap()
	end := lastAddr(p)
	var s []Rec

	if start.Is4() {
		s = t.v4
	} else if start.Is6() {
		s = t.v6
	} else {
		return []Hit{}
	}

	i := sort.Search(len(s), func(i int) bool {
		return s[i].End.Compare(start) >= 0
	})

	seen := map[string]struct{}{}
	out := []Hit{}

	for ; i < len(s) && s[i].Start.Compare(end) <= 0; i++ {
		r := s[i]
		if _, ok := seen[r.Code]; ok {
			continue
		}

		seen[r.Code] = struct{}{}
		out = append(out, Hit{Code: r.Code, Name: r.Name, Prefix: r.Prefix})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Code < out[j].Code })

	return out
}

func Build(in []Entry) *Table {
	var v4, v6 []item

	for _, e := range in {
		it, ok := itemOf(e)
		if !ok {
			continue
		}

		if it.start.Is4() {
			v4 = append(v4, it)
			continue
		}

		if it.start.Is6() {
			v6 = append(v6, it)
		}
	}

	return &Table{v4: assemble(v4), v6: assemble(v6)}
}

type item struct {
	start netip.Addr
	end   netip.Addr
	bits  int
	code  string
	name  string
}

func itemOf(e Entry) (item, bool) {
	if e.Code == "" || !e.Prefix.IsValid() {
		return item{}, false
	}

	p := e.Prefix.Masked()
	start := p.Addr().Unmap()

	if !start.Is4() && !start.Is6() {
		return item{}, false
	}

	return item{
		start: start,
		end:   lastAddr(p),
		bits:  p.Bits(),
		code:  e.Code,
		name:  e.Name,
	}, true
}

type event struct {
	addr netip.Addr
	add  bool
	idx  int
}

func assemble(in []item) []Rec {
	if len(in) == 0 {
		return nil
	}

	events := make([]event, 0, len(in)*2)

	for i, it := range in {
		events = append(events, event{addr: it.start, add: true, idx: i})

		if n, ok := nextAddr(it.end); ok {
			events = append(events, event{addr: n, add: false, idx: i})
		}
	}

	sort.Slice(events, func(i, j int) bool {
		if c := events[i].addr.Compare(events[j].addr); c != 0 {
			return c < 0
		}

		if events[i].add != events[j].add {
			return !events[i].add
		}

		return events[i].idx < events[j].idx
	})

	active := map[int]struct{}{}
	out := make([]Rec, 0, len(in))
	var prev netip.Addr
	havePrev := false

	flush := func(upto netip.Addr) {
		if !havePrev || len(active) == 0 {
			return
		}

		end, ok := prevAddr(upto)
		if !ok || end.Compare(prev) < 0 {
			return
		}

		best, ok := bestOf(in, active)
		if !ok {
			return
		}

		out = append(out, Rec{
			Start: prev, End: end, Code: best.code, Name: best.name,
			Prefix: netip.PrefixFrom(best.start, best.bits),
		})
	}

	for _, ev := range events {
		if havePrev && ev.addr.Compare(prev) > 0 {
			flush(ev.addr)
		}

		if ev.add {
			active[ev.idx] = struct{}{}
		} else {
			delete(active, ev.idx)
		}

		prev = ev.addr
		havePrev = true
	}

	if len(active) > 0 {
		best, ok := bestOf(in, active)
		if ok {
			out = append(out, Rec{
				Start: prev, End: maxAddr(prev), Code: best.code, Name: best.name,
				Prefix: netip.PrefixFrom(best.start, best.bits),
			})
		}
	}

	return mergeSame(out)
}

func bestOf(in []item, active map[int]struct{}) (item, bool) {
	var best item
	found := false

	for i := range active {
		it := in[i]
		if !found || it.bits > best.bits || (it.bits == best.bits && it.code < best.code) {
			best = it
			found = true
		}
	}

	return best, found
}

func mergeSame(in []Rec) []Rec {
	if len(in) == 0 {
		return nil
	}

	out := make([]Rec, 0, len(in))
	cur := in[0]

	for _, next := range in[1:] {
		/*
		 * Склеиваются только куски одного анонса: одинаковый код при разных
		 * префиксах -- это соседние анонсы одной системы, и корзинам они
		 * нужны раздельно. Ценой немного длиннее таблица.
		 */
		if cur.Code == next.Code && cur.Prefix == next.Prefix && touches(cur, next) {
			if next.End.Compare(cur.End) > 0 {
				cur.End = next.End
			}

			if cur.Name == "" {
				cur.Name = next.Name
			}

			continue
		}

		out = append(out, cur)
		cur = next
	}

	return append(out, cur)
}

func touches(a, b Rec) bool {
	if a.End.Compare(b.Start) >= 0 {
		return true
	}

	n, ok := nextAddr(a.End)

	return ok && n == b.Start
}

func lastAddr(p netip.Prefix) netip.Addr {
	p = p.Masked()
	addr := p.Addr().Unmap()
	bits := p.Bits()

	if addr.Is4() {
		n := uint32From4(addr.As4())
		if bits < 32 {
			n |= uint32(1)<<(32-uint(bits)) - 1
		}

		return netip.AddrFrom4(uint32To4(n))
	}

	hi, lo := uint128From16(addr.As16())
	host := 128 - bits

	switch {
	case host <= 0:
	case host < 64:
		lo |= uint64(1)<<uint(host) - 1
	case host == 64:
		lo = ^uint64(0)
	case host < 128:
		lo = ^uint64(0)
		hi |= uint64(1)<<uint(host-64) - 1
	default:
		hi, lo = ^uint64(0), ^uint64(0)
	}

	return netip.AddrFrom16(uint128To16(hi, lo))
}

func nextAddr(a netip.Addr) (netip.Addr, bool) {
	a = a.Unmap()

	if a.Is4() {
		n := uint32From4(a.As4())
		if n == ^uint32(0) {
			return netip.Addr{}, false
		}

		return netip.AddrFrom4(uint32To4(n + 1)), true
	}

	hi, lo := uint128From16(a.As16())
	if lo == ^uint64(0) {
		if hi == ^uint64(0) {
			return netip.Addr{}, false
		}

		return netip.AddrFrom16(uint128To16(hi+1, 0)), true
	}

	return netip.AddrFrom16(uint128To16(hi, lo+1)), true
}

func prevAddr(a netip.Addr) (netip.Addr, bool) {
	a = a.Unmap()

	if a.Is4() {
		n := uint32From4(a.As4())
		if n == 0 {
			return netip.Addr{}, false
		}

		return netip.AddrFrom4(uint32To4(n - 1)), true
	}

	hi, lo := uint128From16(a.As16())
	if lo == 0 {
		if hi == 0 {
			return netip.Addr{}, false
		}

		return netip.AddrFrom16(uint128To16(hi-1, ^uint64(0))), true
	}

	return netip.AddrFrom16(uint128To16(hi, lo-1)), true
}

func maxAddr(sample netip.Addr) netip.Addr {
	if sample.Is4() {
		return netip.AddrFrom4([4]byte{255, 255, 255, 255})
	}

	return netip.AddrFrom16([16]byte{
		255, 255, 255, 255, 255, 255, 255, 255,
		255, 255, 255, 255, 255, 255, 255, 255,
	})
}

func uint32From4(a [4]byte) uint32 {
	return uint32(a[0])<<24 | uint32(a[1])<<16 | uint32(a[2])<<8 | uint32(a[3])
}

func uint32To4(n uint32) [4]byte {
	return [4]byte{byte(n >> 24), byte(n >> 16), byte(n >> 8), byte(n)}
}

func uint128From16(a [16]byte) (hi, lo uint64) {
	for i := 0; i < 8; i++ {
		hi = hi<<8 | uint64(a[i])
		lo = lo<<8 | uint64(a[i+8])
	}

	return hi, lo
}

func uint128To16(hi, lo uint64) [16]byte {
	var out [16]byte

	for i := 7; i >= 0; i-- {
		out[i] = byte(hi)
		hi >>= 8
		out[i+8] = byte(lo)
		lo >>= 8
	}

	return out
}

func ParseASN(code string) (uint32, bool) {
	n, err := strconv.ParseUint(code, 10, 32)
	if err != nil || n == 0 {
		return 0, false
	}

	return uint32(n), true
}
