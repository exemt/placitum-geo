package table

import (
	"net/netip"
	"sort"
)

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

	for _, anns := range x.byPrefix {
		sort.Slice(anns, func(i, j int) bool { return anns[i].Code < anns[j].Code })
	}

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

func (x *Index) Codes() int {
	if x == nil {
		return 0
	}

	return len(x.byCode)
}

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
