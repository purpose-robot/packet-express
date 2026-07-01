package main

import (
	"fmt"
	"net/netip"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jaswdr/faker/v2"
)

// network is an IPv4 or IPv6 network, identified by its masked base address
// and prefix length.
type network struct {
	base   netip.Addr
	prefix int
}

func (n network) key() string {
	return fmt.Sprintf("%s/%d", n.base, n.prefix)
}

// netmaskPrefixLen maps a canonical dotted-decimal subnet mask (e.g.
// "255.255.255.0") to its prefix length. wildcardPrefixLen does the same for
// ACL-style wildcard masks (a mask's bitwise complement, e.g. "0.0.0.255").
// structuralIPv4 is the union of both: values that describe network
// structure rather than a real address, and so are never anonymized.
var (
	netmaskPrefixLen  = map[string]int{}
	wildcardPrefixLen = map[string]int{}
	structuralIPv4    = map[string]struct{}{}
)

func init() {
	for prefixLen := 0; prefixLen <= 32; prefixLen++ {
		var maskBits uint32
		if prefixLen > 0 {
			maskBits = uint32(0xFFFFFFFF) << (32 - prefixLen)
		}
		wildcardBits := ^maskBits

		mask := ipv4StringFromUint32(maskBits)
		wildcard := ipv4StringFromUint32(wildcardBits)

		netmaskPrefixLen[mask] = prefixLen
		wildcardPrefixLen[wildcard] = prefixLen

		structuralIPv4[mask] = struct{}{}
		structuralIPv4[wildcard] = struct{}{}
	}
}

func ipv4StringFromUint32(v uint32) string {
	return netip.AddrFrom4([4]byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}).String()
}

var (
	reIPv4Token = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}(?:/\d{1,2})?\b`)
	reIPv6Token = regexp.MustCompile(`\b[0-9A-Fa-f:]*:[0-9A-Fa-f:]*(?:/\d{1,3})?\b`)

	reIPv4Pair = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})[ \t]+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})\b`)
	reIPv4CIDR = regexp.MustCompile(`\b(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3})/(\d{1,2})\b`)
	reIPv6CIDR = regexp.MustCompile(`\b([0-9A-Fa-f:]+)/(\d{1,3})\b`)
)

// ipAnonymizer produces stable, subnet-preserving fake IPv4/IPv6 addresses.
// Networks declared in the config (interface addresses, routes, ACL
// wildcard masks, CIDR literals) are learned first, so addresses sharing a
// real prefix continue to share a same-length fake prefix -- e.g. both ends
// of a point-to-point link land in the same fake /30. Addresses with no
// discoverable network context fall back to an independent, but still
// consistent, fake host address.
type ipAnonymizer struct {
	fake    faker.Faker
	mapping *Mapping

	v4Networks []network // sorted longest-prefix-first
	v6Networks []network

	fakeNetworks map[string]netip.Addr // "family:real-network" -> fake network base
	usedNetworks map[string]struct{}   // "family:fake-network", to avoid collisions
}

func newIPAnonymizer(fake faker.Faker, mapping *Mapping) *ipAnonymizer {
	return &ipAnonymizer{
		fake:         fake,
		mapping:      mapping,
		fakeNetworks: make(map[string]netip.Addr),
		usedNetworks: make(map[string]struct{}),
	}
}

// learnNetworks scans the whole config text for network declarations: an
// interface address and mask, an ACL address and wildcard mask, or CIDR
// notation. Every unique network found is registered so later substitution
// can preserve subnet structure.
func (a *ipAnonymizer) learnNetworks(text string) {
	seen := make(map[string]struct{})

	add := func(family int, addr netip.Addr, prefix int) {
		if prefix == 0 {
			// A /0 "network" (from a default route's 0.0.0.0 0.0.0.0, or an
			// ACL wildcard matching any address) isn't a real subnet -- it's
			// a wildcard matching the entire address space. Registering it
			// would make every address without more specific context
			// longest-prefix-match into it, and since a /0 has zero network
			// bits, rebase() would then leave those addresses unchanged.
			return
		}

		masked := netip.PrefixFrom(addr, prefix).Masked().Addr()
		key := fmt.Sprintf("%d:%s/%d", family, masked, prefix)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}

		n := network{base: masked, prefix: prefix}
		if family == 4 {
			a.v4Networks = append(a.v4Networks, n)
		} else {
			a.v6Networks = append(a.v6Networks, n)
		}
	}

	for _, m := range reIPv4Pair.FindAllStringSubmatch(text, -1) {
		addr, err := netip.ParseAddr(m[1])
		if err != nil || !addr.Is4() {
			continue
		}

		if prefix, ok := netmaskPrefixLen[m[2]]; ok {
			add(4, addr, prefix)
		} else if prefix, ok := wildcardPrefixLen[m[2]]; ok {
			add(4, addr, prefix)
		}
	}

	for _, m := range reIPv4CIDR.FindAllStringSubmatch(text, -1) {
		addr, err := netip.ParseAddr(m[1])
		if err != nil || !addr.Is4() {
			continue
		}

		prefix, err := strconv.Atoi(m[2])
		if err != nil || prefix < 0 || prefix > 32 {
			continue
		}

		add(4, addr, prefix)
	}

	for _, m := range reIPv6CIDR.FindAllStringSubmatch(text, -1) {
		addr, err := netip.ParseAddr(m[1])
		if err != nil || !addr.Is6() || addr.Is4In6() {
			continue
		}

		prefix, err := strconv.Atoi(m[2])
		if err != nil || prefix < 0 || prefix > 128 {
			continue
		}

		add(6, addr, prefix)
	}

	sort.Slice(a.v4Networks, func(i, j int) bool { return a.v4Networks[i].prefix > a.v4Networks[j].prefix })
	sort.Slice(a.v6Networks, func(i, j int) bool { return a.v6Networks[i].prefix > a.v6Networks[j].prefix })
}

// substitute replaces every IPv4 and IPv6 address found anywhere in text
// with its stable fake counterpart, preserving any CIDR suffix.
func (a *ipAnonymizer) substitute(text string) string {
	text = reIPv4Token.ReplaceAllStringFunc(text, func(m string) string {
		addrPart, prefixPart, hasCIDR := strings.Cut(m, "/")

		fake := a.fakeIPv4(addrPart)
		if hasCIDR {
			return fake + "/" + prefixPart
		}

		return fake
	})

	text = reIPv6Token.ReplaceAllStringFunc(text, func(m string) string {
		addrPart, prefixPart, hasCIDR := strings.Cut(m, "/")

		fake := a.fakeIPv6(addrPart)
		if hasCIDR {
			return fake + "/" + prefixPart
		}

		return fake
	})

	return text
}

func (a *ipAnonymizer) fakeIPv4(real string) string {
	if real == "" {
		return real
	}
	if _, structural := structuralIPv4[real]; structural {
		return real
	}
	if fake, ok := a.mapping.IPv4[real]; ok {
		return fake
	}

	addr, err := netip.ParseAddr(real)
	if err != nil || !addr.Is4() {
		return real
	}

	net, found := a.longestMatch(a.v4Networks, addr)
	if !found {
		net = network{base: addr, prefix: 32}
	}

	fakeNet := a.fakeNetworkFor(net, 4)
	fakeBytes := rebase(addr.AsSlice(), fakeNet.AsSlice(), net.prefix)

	fakeAddr, ok := netip.AddrFromSlice(fakeBytes)
	if !ok {
		return real
	}

	fake := fakeAddr.String()
	a.mapping.IPv4[real] = fake

	return fake
}

func (a *ipAnonymizer) fakeIPv6(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.IPv6[real]; ok {
		return fake
	}

	addr, err := netip.ParseAddr(real)
	if err != nil || !addr.Is6() || addr.Is4In6() {
		return real
	}

	net, found := a.longestMatch(a.v6Networks, addr)
	if !found {
		net = network{base: addr, prefix: 128}
	}

	fakeNet := a.fakeNetworkFor(net, 6)
	fakeBytes := rebase(addr.AsSlice(), fakeNet.AsSlice(), net.prefix)

	fakeAddr, ok := netip.AddrFromSlice(fakeBytes)
	if !ok {
		return real
	}

	fake := fakeAddr.String()
	a.mapping.IPv6[real] = fake

	return fake
}

func (a *ipAnonymizer) longestMatch(networks []network, addr netip.Addr) (network, bool) {
	for _, n := range networks {
		if netip.PrefixFrom(addr, n.prefix).Masked().Addr() == n.base {
			return n, true
		}
	}

	return network{}, false
}

func (a *ipAnonymizer) fakeNetworkFor(real network, family int) netip.Addr {
	key := fmt.Sprintf("%d:%s", family, real.key())

	if fake, ok := a.fakeNetworks[key]; ok {
		return fake
	}

	for {
		bytes := a.randomNetworkBytes(family, real.prefix)

		fakeAddr, ok := netip.AddrFromSlice(bytes)
		if !ok {
			continue
		}

		usedKey := fmt.Sprintf("%d:%s", family, fakeAddr)
		if _, taken := a.usedNetworks[usedKey]; taken {
			continue
		}

		a.usedNetworks[usedKey] = struct{}{}
		a.fakeNetworks[key] = fakeAddr

		return fakeAddr
	}
}

// randomNetworkBytes fabricates a private-space network base of the given
// address family (4 bytes for IPv4, 16 for IPv6): normally anchored within
// 10.0.0.0/8 or fd00::/8, with bits beyond that shared /8 randomized up to
// prefixLen. A learned prefix shorter than that anchor (unusual, but
// possible from a short static route) instead randomizes from bit zero, so
// the fake network's bits are never a deterministic function of the real
// one -- otherwise a real address that also starts with 10 (or fd) would
// rebase back to itself, leaking it unchanged.
func (a *ipAnonymizer) randomNetworkBytes(family, prefixLen int) []byte {
	var out []byte
	var anchorBits int
	if family == 4 {
		out = []byte{10, 0, 0, 0}
		anchorBits = 8
	} else {
		out = make([]byte, 16)
		out[0] = 0xfd
		anchorBits = 8
	}

	totalBits := len(out) * 8
	if prefixLen > totalBits {
		prefixLen = totalBits
	}

	// Every bit rebase() will actually use is in [0, prefixLen); if that
	// whole range sits inside the anchor, randomize from 0 instead, or a
	// prefix exactly as long as the anchor (e.g. a real static route's /8)
	// would leave every used bit fixed and rebase to itself unchanged.
	randomStart := anchorBits
	if prefixLen <= anchorBits {
		randomStart = 0
	}

	for bit := randomStart; bit < prefixLen; bit++ {
		mask := byte(1) << uint(7-bit%8)
		if a.fake.IntBetween(0, 1) == 1 {
			out[bit/8] |= mask
		} else {
			out[bit/8] &^= mask
		}
	}

	return out
}

// rebase preserves the host-portion bits of real while substituting its
// network-portion bits (the top prefixLen bits) with fakeNetwork's. Works
// for both 4-byte (IPv4) and 16-byte (IPv6) addresses.
func rebase(real, fakeNetwork []byte, prefixLen int) []byte {
	out := make([]byte, len(real))
	copy(out, real)

	fullBytes := prefixLen / 8
	if fullBytes > len(out) {
		fullBytes = len(out)
	}
	copy(out[:fullBytes], fakeNetwork[:fullBytes])

	if rem := prefixLen % 8; rem > 0 && fullBytes < len(out) {
		mask := byte(0xFF << uint(8-rem))
		out[fullBytes] = (fakeNetwork[fullBytes] & mask) | (real[fullBytes] & ^mask)
	}

	return out
}
