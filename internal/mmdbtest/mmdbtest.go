/*
 * Package mmdbtest собирает MaxMind DB для тестов.
 *
 * Выгрузки MaxMind в репозиторий не кладутся (лицензия и размер), поэтому
 * базу собирает сам тест: дерево на 24-битных записях, секция данных и
 * метаданные. База IPv6, IPv4 лежит под ::/96 -- как у GeoLite2. Пакет
 * импортируют только тесты, в бинарь он не попадает.
 */

package mmdbtest

import (
	"fmt"
	"net/netip"
	"sort"
)

// BuildEpoch -- сборка тестовой базы в метаданных.
const BuildEpoch = 1723644981

type Network struct {
	CIDR   string
	Record map[string]any
}

type slot struct {
	kind int // 0 -- пусто, 1 -- узел, 2 -- данные
	val  int
}

func Build(dbType string, networks []Network) []byte {
	nodes := [][2]slot{{}}

	set := func(bits []byte, s slot) {
		node := 0

		for _, bit := range bits[:len(bits)-1] {
			next := nodes[node][bit]

			if next.kind == 0 {
				nodes = append(nodes, [2]slot{})
				next = slot{kind: 1, val: len(nodes) - 1}
				nodes[node][bit] = next
			}

			if next.kind != 1 {
				panic("mmdbtest: network under a network")
			}

			node = next.val
		}

		nodes[node][bits[len(bits)-1]] = s
	}

	var data []byte
	offsets := map[string]int{}

	for _, n := range networks {
		key := fmt.Sprint(n.Record)
		offset, ok := offsets[key]

		if !ok {
			offset = len(data)
			data = append(data, encode(n.Record)...)
			offsets[key] = offset
		}

		set(bitsOf(n.CIDR), slot{kind: 2, val: offset})
	}

	count := len(nodes)
	out := make([]byte, 0, count*6+16+len(data)+256)

	for _, pair := range nodes {
		for _, s := range pair {
			v := count

			switch s.kind {
			case 1:
				v = s.val
			case 2:
				v = count + 16 + s.val
			}

			out = append(out, byte(v>>16), byte(v>>8), byte(v))
		}
	}

	out = append(out, make([]byte, 16)...)
	out = append(out, data...)
	out = append(out, "\xab\xcd\xefMaxMind.com"...)

	return append(out, encode(map[string]any{
		"node_count":                  count,
		"record_size":                 24,
		"ip_version":                  6,
		"database_type":               dbType,
		"build_epoch":                 BuildEpoch,
		"binary_format_major_version": 2,
		"binary_format_minor_version": 0,
		"languages":                   []any{"en"},
		"description":                 map[string]any{"en": "placitum test database"},
	})...)
}

func bitsOf(cidr string) []byte {
	p := netip.MustParsePrefix(cidr).Masked()
	addr, n := p.Addr(), p.Bits()

	var raw []byte

	if addr.Is4() {
		b := addr.As4()
		raw = append(make([]byte, 12), b[:]...)
		n += 96
	} else {
		b := addr.As16()
		raw = b[:]
	}

	bits := make([]byte, n)

	for i := range bits {
		bits[i] = raw[i/8] >> (7 - i%8) & 1
	}

	return bits
}

func encode(v any) []byte {
	switch x := v.(type) {
	case string:
		return append(control(2, len(x)), x...)
	case int:
		return encodeUint(uint64(x))
	case uint64:
		return encodeUint(x)
	case []any:
		out := control(11, len(x))
		for _, item := range x {
			out = append(out, encode(item)...)
		}

		return out
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		out := control(7, len(x))
		for _, k := range keys {
			out = append(out, encode(k)...)
			out = append(out, encode(x[k])...)
		}

		return out
	}

	panic(fmt.Sprintf("mmdbtest: cannot encode %T", v))
}

func encodeUint(n uint64) []byte {
	var b []byte
	for ; n > 0; n >>= 8 {
		b = append([]byte{byte(n)}, b...)
	}

	typ := 6
	if len(b) > 4 {
		typ = 9
	}

	return append(control(typ, len(b)), b...)
}

func control(typ, size int) []byte {
	bits, tail := size, []byte(nil)

	switch {
	case size >= 65821:
		r := size - 65821
		bits, tail = 31, []byte{byte(r >> 16), byte(r >> 8), byte(r)}
	case size >= 285:
		r := size - 285
		bits, tail = 30, []byte{byte(r >> 8), byte(r)}
	case size >= 29:
		bits, tail = 29, []byte{byte(size - 29)}
	}

	if typ > 7 {
		return append([]byte{byte(bits), byte(typ - 7)}, tail...)
	}

	return append([]byte{byte(typ<<5 | bits)}, tail...)
}
