// Command anonymizer rewrites a neighbors view (as produced by the transformer)
// into a topologically faithful but fictional copy. Sensitive identifiers
// (hostnames, management addresses, serial numbers and software versions) are
// replaced with realistic fake values so the data can be shared with Claude
// without exposing the real internal network.
//
// The replacement is consistent: a given real value always maps to the same
// fake value, so cross-references between devices and their neighbors stay
// intact and the graph remains coherent. Non-sensitive structural fields
// (platform model, interface names) are preserved unchanged so the topology is
// still useful to reason about.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jaswdr/faker/v2"
)

// StackMember mirrors the transformer's StackMember so the JSON round-trips
// with identical structure.
type StackMember struct {
	ID              int    `json:"id"`
	Role            string `json:"role,omitempty"`
	SerialNumber    string `json:"serial_number"`
	SoftwareVersion string `json:"software_version"`
}

// Device mirrors the transformer's Device.
type Device struct {
	Platform        string        `json:"platform"`
	Hostname        string        `json:"hostname"`
	IPAddress       string        `json:"ip_address"`
	StackMembers    []StackMember `json:"stack_members,omitempty"`
	SerialNumber    string        `json:"serial_number,omitempty"`
	SoftwareVersion string        `json:"software_version,omitempty"`
}

// Neighbor mirrors the transformer's Neighbor.
type Neighbor struct {
	LocalHostname   string `json:"local_hostname"`
	LocalInterface  string `json:"local_interface"`
	LocalIPAddress  string `json:"local_ip_address"`
	RemotePlatform  string `json:"remote_platform"`
	RemoteHostname  string `json:"remote_hostname"`
	RemoteInterface string `json:"remote_interface"`
	RemoteIPAddress string `json:"remote_ip_address"`
}

// LocationView mirrors the transformer's LocationView.
type LocationView struct {
	Devices   []Device   `json:"devices"`
	Neighbors []Neighbor `json:"neighbors"`
}

// anonymizer produces stable fake replacements for each kind of sensitive
// value. Every map remembers what a real value was rewritten to, so the same
// input always yields the same output and the topology's cross-references are
// preserved.
type anonymizer struct {
	fake faker.Faker

	hostnames map[string]string // keyed by lower-cased real hostname
	ips       map[string]string
	serials   map[string]string
	versions  map[string]string

	usedHostnames map[string]struct{}
	usedIPs       map[string]struct{}
	usedSerials   map[string]struct{}

	hostCount int
}

func newAnonymizer(fake faker.Faker) *anonymizer {
	return &anonymizer{
		fake:          fake,
		hostnames:     make(map[string]string),
		ips:           make(map[string]string),
		serials:       make(map[string]string),
		versions:      make(map[string]string),
		usedHostnames: make(map[string]struct{}),
		usedIPs:       make(map[string]struct{}),
		usedSerials:   make(map[string]struct{}),
	}
}

// siteRoles supply realistic network-device role tokens for generated hostnames.
var siteRoles = []string{"CORE", "DIST", "ACC-SW", "AGG", "RTR", "FW", "EDGE"}

// serialPrefixes are common Cisco manufacturing-location codes, used to keep
// generated serial numbers looking authentic.
var serialPrefixes = []string{"FOC", "FCW", "FJC", "FXS", "FDO", "FGL"}

// hostname returns a stable fake hostname for a real one. Empty input maps to
// empty output so optional fields stay optional.
func (a *anonymizer) hostname(real string) string {
	if real == "" {
		return ""
	}

	key := strings.ToLower(real)
	if fake, ok := a.hostnames[key]; ok {
		return fake
	}

	fake := a.newHostname()
	a.hostnames[key] = fake

	return fake
}

// newHostname builds a unique, realistic device hostname such as
// "DE-MUC-CORE01" following the common <country>-<site>-<role><nn> convention.
func (a *anonymizer) newHostname() string {
	for {
		country := strings.ToUpper(a.fake.Address().CountryAbbr())
		if len(country) > 2 {
			country = country[:2]
		}

		site := siteCode(a.fake.Address().City())
		role := a.fake.RandomStringElement(siteRoles)

		a.hostCount++
		candidate := fmt.Sprintf("%s-%s-%s%02d", country, site, role, a.hostCount)

		if _, taken := a.usedHostnames[candidate]; taken {
			continue
		}

		a.usedHostnames[candidate] = struct{}{}

		return candidate
	}
}

// siteCode reduces a city name to a 3-letter uppercase site abbreviation.
func siteCode(city string) string {
	var letters []rune
	for _, r := range city {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			letters = append(letters, r)
		}
		if len(letters) == 3 {
			break
		}
	}

	for len(letters) < 3 {
		letters = append(letters, 'X')
	}

	return strings.ToUpper(string(letters))
}

// ip returns a stable fake RFC1918 management address for a real one.
func (a *anonymizer) ip(real string) string {
	if real == "" {
		return ""
	}

	if fake, ok := a.ips[real]; ok {
		return fake
	}

	var fake string
	for {
		fake = a.fake.Internet().LocalIpv4()
		if _, taken := a.usedIPs[fake]; !taken {
			break
		}
	}

	a.usedIPs[fake] = struct{}{}
	a.ips[real] = fake

	return fake
}

// serial returns a stable fake Cisco-style serial number for a real one.
func (a *anonymizer) serial(real string) string {
	if real == "" {
		return ""
	}

	if fake, ok := a.serials[real]; ok {
		return fake
	}

	var fake string
	for {
		prefix := a.fake.RandomStringElement(serialPrefixes)
		// Four-digit year/week code followed by a four-character lot suffix,
		// matching the 11-character Cisco serial layout (e.g. FCW2301AA04).
		fake = prefix + a.fake.Numerify("####") + strings.ToUpper(a.fake.Bothify("??#?"))
		if _, taken := a.usedSerials[fake]; !taken {
			break
		}
	}

	a.usedSerials[fake] = struct{}{}
	a.serials[real] = fake

	return fake
}

// version returns a stable fake IOS-XE style software version for a real one.
func (a *anonymizer) version(real string) string {
	if real == "" {
		return ""
	}

	if fake, ok := a.versions[real]; ok {
		return fake
	}

	fake := fmt.Sprintf("17.%02d.%02d", a.fake.IntBetween(1, 15), a.fake.IntBetween(1, 12))
	a.versions[real] = fake

	return fake
}

// anonymizeView rewrites every sensitive field of a location view in place.
func (a *anonymizer) anonymizeView(lv *LocationView) {
	for i := range lv.Devices {
		d := &lv.Devices[i]
		d.Hostname = a.hostname(d.Hostname)
		d.IPAddress = a.ip(d.IPAddress)
		d.SerialNumber = a.serial(d.SerialNumber)
		d.SoftwareVersion = a.version(d.SoftwareVersion)

		for j := range d.StackMembers {
			m := &d.StackMembers[j]
			m.SerialNumber = a.serial(m.SerialNumber)
			m.SoftwareVersion = a.version(m.SoftwareVersion)
		}
	}

	for i := range lv.Neighbors {
		n := &lv.Neighbors[i]
		n.LocalHostname = a.hostname(n.LocalHostname)
		n.LocalIPAddress = a.ip(n.LocalIPAddress)
		n.RemoteHostname = a.hostname(n.RemoteHostname)
		n.RemoteIPAddress = a.ip(n.RemoteIPAddress)
	}
}

func main() {
	inputFile := flag.String("input", "topology.json", "neighbors view JSON to anonymize")
	outputFile := flag.String("output", "topology_anon.json", "file to write the anonymized view to")
	seed := flag.Int64("seed", 0, "optional fixed seed for reproducible output (0 = random)")

	flag.Parse()

	raw, err := os.ReadFile(*inputFile)
	if err != nil {
		log.Fatalf("failed to read input file %s; %v", *inputFile, err)
	}

	var view map[string]*LocationView
	if err := json.Unmarshal(raw, &view); err != nil {
		log.Fatalf("failed to parse input file %s; %v", *inputFile, err)
	}

	fake := faker.New()
	if *seed != 0 {
		fake = faker.NewWithSeedInt64(*seed)
	}

	anon := newAnonymizer(fake)

	// Sensitive values are shared across locations (a device in one location is
	// a neighbor in another), so a single anonymizer maps them consistently for
	// the whole file.
	for _, lv := range view {
		anon.anonymizeView(lv)
	}

	rendered, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal anonymized view; %v", err)
	}

	if err := os.WriteFile(*outputFile, append(rendered, '\n'), 0644); err != nil {
		log.Fatalf("failed to write anonymized view to %s; %v", *outputFile, err)
	}

	log.Printf("anonymized %d location(s) from %s to %s", len(view), *inputFile, *outputFile)
}
