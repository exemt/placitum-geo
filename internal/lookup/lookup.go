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

type Range struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type ASN struct {
	ASN       uint32   `json:"asn"`
	Name      string   `json:"name,omitempty"`
	Prefix    string   `json:"prefix,omitempty"`
	Effective bool     `json:"effective,omitempty"`
	Range     *Range   `json:"range,omitempty"`
	Prefixes  []string `json:"prefixes,omitempty"`
}

type Result struct {
	Gen       uint64    `json:"gen"`
	Countries []Country `json:"countries"`
	ASNs      []ASN     `json:"asns"`
}

type Options struct {
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

type BatchItem struct {
	Addr      string    `json:"addr"`
	Countries []Country `json:"countries"`
	ASNs      []ASN     `json:"asns"`
	Error     string    `json:"error,omitempty"`
}

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

	if hasHit && !marked {
		if n, ok := table.ParseASN(hit.Code); ok {
			row := ASN{ASN: n, Name: hit.Name, Effective: true, Range: rangeOf(hit)}

			if hit.Prefix.IsValid() {
				row.Prefix = hit.Prefix.String()
			}

			rows = append([]ASN{row}, rows...)
		}
	}

	sort.SliceStable(rows, func(i, j int) bool { return rows[i].Effective && !rows[j].Effective })

	return rows
}

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
