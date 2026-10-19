package lists

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"github.com/sagernet/sing/common/domain"
	"go4.org/netipx"
	"io"
	"net/netip"
)

const maxNativeExpanded = 32 << 20
const maxNativeLabels = 1 << 20

// Preflight the supported binary grammar before the upstream reader allocates
// from untrusted counts. Complete decompression verifies EOF/checksum first.
func preflightSRS(body []byte) error {
	bad := func(s string) error { return fmt.Errorf("%w: SRS %s", ErrUnsupportedNative, s) }
	if len(body) < 5 || string(body[:3]) != "SRS" || body[3] < 1 || body[3] > 3 {
		return bad("header/version")
	}
	input := bytes.NewReader(body[4:])
	zr, err := zlib.NewReader(input)
	if err != nil {
		return bad("compression header")
	}
	expanded, err := io.ReadAll(io.LimitReader(zr, maxNativeExpanded+1))
	closeErr := zr.Close()
	if err != nil || closeErr != nil || len(expanded) > maxNativeExpanded || input.Len() != 0 {
		return bad("compression size/checksum/trailing data")
	}
	p := srsPreflight{r: bytes.NewReader(expanded), version: body[3]}
	count, err := p.count(maxNativeNodes)
	if err != nil || count == 0 {
		return bad("rule count")
	}
	for i := 0; i < count; i++ {
		if err := p.rule(0); err != nil {
			return bad(err.Error())
		}
	}
	if p.r.Len() != 0 {
		return bad("trailing expanded data")
	}
	return nil
}

type srsPreflight struct {
	r              *bytes.Reader
	nodes, values  int
	recoveredBytes int
	version        byte
}

func (p *srsPreflight) count(limit int) (int, error) {
	n, e := binary.ReadUvarint(p.r)
	if e != nil || n > uint64(limit) {
		return 0, fmt.Errorf("count budget")
	}
	return int(n), nil
}
func (p *srsPreflight) byte() (byte, error) { return p.r.ReadByte() }
func (p *srsPreflight) boolean() error {
	v, e := p.byte()
	if e != nil || v > 1 {
		return fmt.Errorf("invalid boolean")
	}
	return nil
}
func (p *srsPreflight) strings() error {
	n, e := p.count(maxNativeValues - p.values)
	if e != nil || n == 0 {
		return fmt.Errorf("string count")
	}
	p.values += n
	for i := 0; i < n; i++ {
		size, e := p.count(4096)
		if e != nil || size == 0 || size > p.r.Len() {
			return fmt.Errorf("string size")
		}
		_, _ = p.r.Seek(int64(size), io.SeekCurrent)
	}
	return nil
}
func (p *srsPreflight) rule(depth int) error {
	p.nodes++
	if depth > maxNativeDepth || p.nodes > maxNativeNodes {
		return fmt.Errorf("node/depth budget")
	}
	kind, e := p.byte()
	if e != nil {
		return e
	}
	if kind == 1 {
		mode, e := p.byte()
		if e != nil || mode > 1 {
			return fmt.Errorf("logical mode")
		}
		n, e := p.count(maxNativeNodes - p.nodes)
		if e != nil || n == 0 {
			return fmt.Errorf("logical count")
		}
		for i := 0; i < n; i++ {
			if e := p.rule(depth + 1); e != nil {
				return e
			}
		}
		return p.boolean()
	}
	if kind != 0 {
		return fmt.Errorf("rule kind")
	}
	seen := map[byte]bool{}
	for {
		item, e := p.byte()
		if e != nil {
			return e
		}
		if item == 255 {
			return p.boolean()
		}
		if seen[item] {
			return fmt.Errorf("duplicate item")
		}
		seen[item] = true
		switch item {
		case 2:
			e = p.matcher()
		case 3, 4:
			e = p.strings()
		case 6:
			e = p.ipset()
		default:
			return fmt.Errorf("predicate item %d", item)
		}
		if e != nil {
			return e
		}
	}
}
func (p *srsPreflight) words() ([]uint64, error) {
	n, e := p.count(maxNativeLabels / 8)
	if e != nil || n > p.r.Len()/8 {
		return nil, fmt.Errorf("bitmap size")
	}
	v := make([]uint64, n)
	for i := range v {
		if e = binary.Read(p.r, binary.BigEndian, &v[i]); e != nil {
			return nil, e
		}
	}
	return v, nil
}
func bit(words []uint64, i int) bool {
	return i >= 0 && i/64 < len(words) && (words[i/64]&(1<<uint(i%64))) != 0
}
func (p *srsPreflight) matcher() error {
	start := p.r.Size() - int64(p.r.Len())
	reserved, e := p.byte()
	if e != nil || reserved != 0 {
		return fmt.Errorf("matcher header")
	}
	leaves, e := p.words()
	if e != nil {
		return e
	}
	bitmap, e := p.words()
	if e != nil {
		return e
	}
	n, e := p.count(maxNativeLabels)
	if e != nil || n == 0 || n > p.r.Len() {
		return fmt.Errorf("labels size")
	}
	labels := make([]byte, n)
	_, e = io.ReadFull(p.r, labels)
	if e != nil {
		return e
	}
	// LOUDS is a breadth-first rooted tree: N labels, N+1 nodes, each node
	// terminates with a one. Enforce complete structure before matcher.Dump.
	nodes := n + 1
	if len(leaves) != (nodes+63)/64 || len(bitmap) != (2*n+1+63)/64 || bit(leaves, 0) {
		return fmt.Errorf("matcher bitmap shape")
	}
	depths := make([]int, nodes)
	labelIndex, bitIndex, next := 0, 0, 1
	for node := 0; node < nodes; node++ {
		if node >= next {
			return fmt.Errorf("unreachable matcher node")
		}
		last := -1
		for !bit(bitmap, bitIndex) {
			if bitIndex >= 2*n+1 || labelIndex >= n || next >= nodes {
				return fmt.Errorf("invalid matcher edges")
			}
			label := int(labels[labelIndex])
			if label <= last {
				return fmt.Errorf("unordered matcher labels")
			}
			last = label
			depths[next] = depths[node] + 1
			if depths[next] > 256 {
				return fmt.Errorf("domain depth budget")
			}
			next++
			labelIndex++
			bitIndex++
		}
		bitIndex++
	}
	if labelIndex != n || bitIndex != 2*n+1 || next != nodes {
		return fmt.Errorf("incomplete matcher")
	}
	for i := 0; i < nodes; i++ {
		if bit(leaves, i) {
			p.values++
			p.recoveredBytes += depths[i]
			if p.values > maxNativeValues || p.recoveredBytes > 8<<20 {
				return fmt.Errorf("recovered domain budget")
			}
		}
	}
	for i := nodes; i < len(leaves)*64; i++ {
		if bit(leaves, i) {
			return fmt.Errorf("leaf padding")
		}
	}
	for i := bitIndex; i < len(bitmap)*64; i++ {
		if bit(bitmap, i) {
			return fmt.Errorf("bitmap padding")
		}
	}
	end := p.r.Size() - int64(p.r.Len())
	raw := make([]byte, end-start)
	_, _ = p.r.ReadAt(raw, start)
	matcher, err := domain.ReadMatcher(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("compiled domain decode")
	}
	domains, suffixes := matcher.Dump()
	var canonical bytes.Buffer
	if err = domain.NewMatcher(domains, suffixes, p.version == 1).Write(&canonical); err != nil || !bytes.Equal(raw, canonical.Bytes()) {
		return fmt.Errorf("noncanonical compiled domain matcher")
	}
	return nil
}
func (p *srsPreflight) ipset() error {
	version, e := p.byte()
	if e != nil || version != 1 {
		return fmt.Errorf("IP set version")
	}
	var n uint64
	if e = binary.Read(p.r, binary.BigEndian, &n); e != nil || n > uint64(maxNativeValues-p.values) {
		return fmt.Errorf("IP set count")
	}
	p.values += int(n)
	var previous netip.Addr
	for i := uint64(0); i < n; i++ {
		addresses := [2]netip.Addr{}
		for j := range addresses {
			size, e := p.count(16)
			if e != nil || (size != 4 && size != 16) {
				return fmt.Errorf("IP address size")
			}
			b := make([]byte, size)
			if _, e = io.ReadFull(p.r, b); e != nil {
				return e
			}
			addresses[j], _ = netip.AddrFromSlice(b)
		}
		a, b := addresses[0], addresses[1]
		if a.BitLen() != b.BitLen() || a.Compare(b) > 0 || (previous.IsValid() && previous.Compare(a) >= 0) {
			return fmt.Errorf("IP range order")
		}
		p.values += len(netipx.IPRangeFrom(a, b).Prefixes())
		if p.values > maxNativeValues {
			return fmt.Errorf("IP projection budget")
		}
		previous = b
	}
	return nil
}
