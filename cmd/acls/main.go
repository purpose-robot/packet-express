// Command acl-randomizer anonymizes and de-anonymizes the IPv4 addresses and
// access-list names in "show ip access-list" output from a Cisco IOS XE
// device, so it can be shared (TAC, a forum, a colleague) without leaking
// real addresses or naming conventions, and later restored once help has
// been received.
//
// Usage:
//
//	acl-randomizer anonymize   [-map=map.json] [file]
//	acl-randomizer deanonymize [-map=map.json] [file]
//
// If [file] is omitted, input is read from stdin. Output always goes to
// stdout. The mapping file is created/extended by anonymize and required by
// deanonymize.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/bits"
	"net/netip"
	"os"
	"regexp"
)

var ipv4Pattern = regexp.MustCompile(`\b\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}\b`)

// aclHeaderPattern matches a "show ip access-list" header line, e.g.
// "Extended IP access list OUTSIDE-IN" or "Standard IP access list 10".
// Group 1 is the fixed prefix, group 2 is the ACL's number or name.
var aclHeaderPattern = regexp.MustCompile(`^((?:Standard|Extended) IP access list )(\S+)`)

var numericToken = regexp.MustCompile(`^\d+$`)

// mapping is the on-disk record of real -> fake substitutions. Only that
// direction is persisted; the reverse lookups needed for de-anonymization
// are derived from it in memory.
type mapping struct {
	RealToFake     map[string]string `json:"real_to_fake"`
	RealToFakeName map[string]string `json:"real_to_fake_name"`
}

func loadMapping(path string) (*mapping, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return &mapping{RealToFake: map[string]string{}, RealToFakeName: map[string]string{}}, nil
	}
	if err != nil {
		return nil, err
	}
	m := &mapping{}
	if err := json.Unmarshal(data, m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m.RealToFake == nil {
		m.RealToFake = map[string]string{}
	}
	if m.RealToFakeName == nil {
		m.RealToFakeName = map[string]string{}
	}
	return m, nil
}

func (m *mapping) save(path string) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

// isMaskLike reports whether addr is a plausible subnet mask (contiguous
// leading 1 bits, e.g. 255.255.255.0) or wildcard mask (contiguous trailing
// 1 bits, e.g. 0.0.0.255). Every network line in a Cisco ACL pairs an
// address with one of these, and substituting a fake IP in place of the
// mask would make the anonymized output look broken without adding any
// privacy benefit, since a mask alone doesn't identify a network. This is a
// heuristic rather than a real parse of ACL grammar: a genuine host address
// that happens to sit on a power-of-two boundary (e.g. 128.0.0.0) would also
// be left untouched, but that's not a realistic address to see in practice.
func isMaskLike(addr netip.Addr) bool {
	b := addr.As4()
	v := uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3])

	leadingOnes := bits.LeadingZeros32(^v)
	if v == ^uint32(0)<<(32-leadingOnes) {
		return true
	}

	trailingOnes := bits.TrailingZeros32(^v)
	if trailingOnes >= 32 {
		return true
	}
	return v == (uint32(1)<<trailingOnes)-1
}

// fakeIPGenerator hands out addresses from the IPv4 ranges RFC 5737 reserves
// for documentation, so anonymized output can never collide with a real
// address appearing elsewhere in the same ACL.
type fakeIPGenerator struct {
	blocks [3]netip.Addr
	n      int
}

func newFakeIPGenerator(startAt int) *fakeIPGenerator {
	return &fakeIPGenerator{
		blocks: [3]netip.Addr{
			netip.MustParseAddr("192.0.2.0"),
			netip.MustParseAddr("198.51.100.0"),
			netip.MustParseAddr("203.0.113.0"),
		},
		n: startAt,
	}
}

func (g *fakeIPGenerator) next() (netip.Addr, error) {
	const perBlock = 254 // usable host part per /24: .1 - .254
	if g.n >= perBlock*len(g.blocks) {
		return netip.Addr{}, fmt.Errorf("exhausted all %d reserved documentation addresses", perBlock*len(g.blocks))
	}
	block := g.blocks[g.n/perBlock].As4()
	block[3] = byte(g.n%perBlock) + 1
	g.n++
	return netip.AddrFrom4(block), nil
}

// fakeNameGenerator hands out short, obviously-synthetic ACL names so they
// can't be confused with a real one.
type fakeNameGenerator struct {
	n int
}

func newFakeNameGenerator(startAt int) *fakeNameGenerator {
	return &fakeNameGenerator{n: startAt}
}

func (g *fakeNameGenerator) next() string {
	g.n++
	return fmt.Sprintf("ACL-%d", g.n)
}

// anonymizeACLName rewrites the name in a "show ip access-list" header line,
// e.g. "Extended IP access list OUTSIDE-IN". Purely numeric names are left
// alone: those are ACL numbers, not identifiers chosen by an administrator,
// so they carry no information worth hiding.
func anonymizeACLName(line string, m *mapping, gen *fakeNameGenerator) string {
	match := aclHeaderPattern.FindStringSubmatch(line)
	if match == nil || numericToken.MatchString(match[2]) {
		return line
	}
	name := match[2]
	fake, ok := m.RealToFakeName[name]
	if !ok {
		fake = gen.next()
		m.RealToFakeName[name] = fake
	}
	return match[1] + fake + line[len(match[0]):]
}

// deanonymizeACLName reverses anonymizeACLName using the fake->real lookup.
func deanonymizeACLName(line string, fakeToRealName map[string]string) string {
	match := aclHeaderPattern.FindStringSubmatch(line)
	if match == nil {
		return line
	}
	real, ok := fakeToRealName[match[2]]
	if !ok {
		return line
	}
	return match[1] + real + line[len(match[0]):]
}

func anonymizeText(r io.Reader, w io.Writer, m *mapping) error {
	gen := newFakeIPGenerator(len(m.RealToFake))
	nameGen := newFakeNameGenerator(len(m.RealToFakeName))
	bw := bufio.NewWriter(w)
	defer bw.Flush()

	var genErr error
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := anonymizeACLName(scanner.Text(), m, nameGen)
		line = ipv4Pattern.ReplaceAllStringFunc(line, func(tok string) string {
			if genErr != nil {
				return tok
			}
			addr, err := netip.ParseAddr(tok)
			if err != nil {
				return tok // not actually a valid IPv4 address
			}
			if isMaskLike(addr) {
				return tok
			}
			if fake, ok := m.RealToFake[tok]; ok {
				return fake
			}
			fake, err := gen.next()
			if err != nil {
				genErr = err
				return tok
			}
			m.RealToFake[tok] = fake.String()
			return fake.String()
		})
		fmt.Fprintln(bw, line)
	}
	if genErr != nil {
		return genErr
	}
	return scanner.Err()
}

func deanonymizeText(r io.Reader, w io.Writer, m *mapping) error {
	fakeToReal := make(map[string]string, len(m.RealToFake))
	for real, fake := range m.RealToFake {
		fakeToReal[fake] = real
	}
	fakeToRealName := make(map[string]string, len(m.RealToFakeName))
	for real, fake := range m.RealToFakeName {
		fakeToRealName[fake] = real
	}

	bw := bufio.NewWriter(w)
	defer bw.Flush()

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := deanonymizeACLName(scanner.Text(), fakeToRealName)
		line = ipv4Pattern.ReplaceAllStringFunc(line, func(tok string) string {
			if real, ok := fakeToReal[tok]; ok {
				return real
			}
			return tok
		})
		fmt.Fprintln(bw, line)
	}
	return scanner.Err()
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  acl-randomizer anonymize   [-map=map.json] [file]
  acl-randomizer deanonymize [-map=map.json] [file]

If [file] is omitted, input is read from stdin; output always goes to stdout.`)
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	mode := os.Args[1]
	if mode != "anonymize" && mode != "deanonymize" {
		usage()
		os.Exit(2)
	}

	fs := flag.NewFlagSet(mode, flag.ExitOnError)
	mapPath := fs.String("map", "acl-anon-map.json", "path to the IP mapping file")
	fs.Parse(os.Args[2:])

	var in io.Reader = os.Stdin
	if args := fs.Args(); len(args) > 0 {
		f, err := os.Open(args[0])
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		defer f.Close()
		in = f
	}

	if mode == "deanonymize" {
		if _, err := os.Stat(*mapPath); err != nil {
			fmt.Fprintf(os.Stderr, "error: mapping file %s not found, nothing to de-anonymize with\n", *mapPath)
			os.Exit(1)
		}
	}

	m, err := loadMapping(*mapPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}

	switch mode {
	case "anonymize":
		err = anonymizeText(in, os.Stdout, m)
		if err == nil {
			err = m.save(*mapPath)
		}
	case "deanonymize":
		err = deanonymizeText(in, os.Stdout, m)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
