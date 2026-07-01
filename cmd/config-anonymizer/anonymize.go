package main

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/jaswdr/faker/v2"
)

// siteRoles supply realistic network-device role tokens for generated
// hostnames.
var siteRoles = []string{"CORE", "DIST", "ACC-SW", "AGG", "RTR", "FW", "EDGE"}

var (
	reCertChainStart = regexp.MustCompile(`^\s*certificate\s+(?:self-signed|ca)\s+\S+\s*$`)
	reBannerStart    = regexp.MustCompile(`^(\s*banner\s+\S+\s+)(.)(.*)$`)
	reMAC            = regexp.MustCompile(`\b[0-9A-Fa-f]{4}\.[0-9A-Fa-f]{4}\.[0-9A-Fa-f]{4}\b`)
)

// configAnonymizer produces stable fake replacements for every sensitive
// value found in a Cisco IOS XE running-config. Every generator remembers
// what a real value was rewritten to (in mapping), so repeated occurrences
// of the same value -- even across unrelated commands -- are always
// rewritten the same way.
type configAnonymizer struct {
	fake    faker.Faker
	mapping *Mapping
	ip      *ipAnonymizer

	usedHostnames map[string]struct{}
	usedDomains   map[string]struct{}
	usedUsernames map[string]struct{}
	usedMACs      map[string]struct{}
	usedASNs      map[string]struct{}

	hostCount int
}

func newConfigAnonymizer(fake faker.Faker) *configAnonymizer {
	mapping := newMapping()

	return &configAnonymizer{
		fake:    fake,
		mapping: mapping,
		ip:      newIPAnonymizer(fake, mapping),

		usedHostnames: make(map[string]struct{}),
		usedDomains:   make(map[string]struct{}),
		usedUsernames: make(map[string]struct{}),
		usedMACs:      make(map[string]struct{}),
		usedASNs:      make(map[string]struct{}),
	}
}

// Anonymize rewrites every sensitive value in a running-config's text.
// Certificates and banners span multiple lines, so the config is scanned
// with a small amount of state; every other construct is handled one line
// at a time. IPv4/IPv6/MAC addresses are substituted in a final pass over
// the whole reconstructed text so every occurrence is caught regardless of
// which command embeds it.
func (a *configAnonymizer) Anonymize(text string) string {
	a.ip.learnNetworks(text)

	lines := strings.Split(text, "\n")
	out := make([]string, 0, len(lines))

	inCertificate := false
	inBanner := false
	var bannerDelim byte
	var bannerLines []string

	for _, line := range lines {
		if inCertificate {
			if strings.TrimSpace(line) == "quit" {
				out = append(out, "  <certificate redacted>", line)
				inCertificate = false
				a.redactSecret()
			}
			continue
		}

		if inBanner {
			if idx := strings.IndexByte(line, bannerDelim); idx >= 0 {
				out = append(out, a.freeText(strings.Join(bannerLines, "\n")), line)
				inBanner = false
				continue
			}
			bannerLines = append(bannerLines, line)
			continue
		}

		if reCertChainStart.MatchString(line) {
			out = append(out, line)
			inCertificate = true
			continue
		}

		if resultLine, isBanner, needsBody, delim := a.tryEnterBanner(line); isBanner {
			out = append(out, resultLine)
			if needsBody {
				inBanner = true
				bannerDelim = delim
				bannerLines = nil
			}
			continue
		}

		out = append(out, a.processLine(line))
	}

	result := strings.Join(out, "\n")
	result = a.ip.substitute(result)
	result = reMAC.ReplaceAllStringFunc(result, a.mac)

	return result
}

// tryEnterBanner recognises a "banner <type> <delim>..." line. A banner
// closed by its delimiter on the same line is resolved immediately; one left
// open needs its body collected from subsequent lines until the delimiter
// reappears.
func (a *configAnonymizer) tryEnterBanner(line string) (resultLine string, isBanner bool, needsBody bool, delim byte) {
	m := reBannerStart.FindStringSubmatch(line)
	if m == nil {
		return "", false, false, 0
	}

	prefix, delimStr, rest := m[1], m[2], m[3]
	delim = delimStr[0]

	if idx := strings.IndexByte(rest, delim); idx >= 0 {
		real := rest[:idx]
		return prefix + delimStr + a.freeText(real) + rest[idx:], true, false, 0
	}

	return prefix + delimStr + rest, true, true, delim
}

func (a *configAnonymizer) hostname(real string) string {
	if real == "" {
		return real
	}

	if fake, ok := a.mapping.Hostnames[real]; ok {
		return fake
	}

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
		a.mapping.Hostnames[real] = candidate

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

func (a *configAnonymizer) domain(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.Domains[real]; ok {
		return fake
	}

	var fake string
	for {
		fake = a.fake.Internet().Domain()
		if _, taken := a.usedDomains[fake]; !taken {
			break
		}
	}

	a.usedDomains[fake] = struct{}{}
	a.mapping.Domains[real] = fake

	return fake
}

func (a *configAnonymizer) username(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.Usernames[real]; ok {
		return fake
	}

	var fake string
	for {
		fake = a.fake.Internet().User()
		if _, taken := a.usedUsernames[fake]; !taken {
			break
		}
	}

	a.usedUsernames[fake] = struct{}{}
	a.mapping.Usernames[real] = fake

	return fake
}

func (a *configAnonymizer) vrf(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.VRFs[real]; ok {
		return fake
	}

	fake := fmt.Sprintf("VRF-%02d", len(a.mapping.VRFs)+1)
	a.mapping.VRFs[real] = fake

	return fake
}

func (a *configAnonymizer) asn(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.ASNs[real]; ok {
		return fake
	}

	var fake string
	for {
		fake = fmt.Sprintf("%d", a.fake.UIntBetween(64512, 65534))
		if _, taken := a.usedASNs[fake]; !taken {
			break
		}
	}

	a.usedASNs[fake] = struct{}{}
	a.mapping.ASNs[real] = fake

	return fake
}

func (a *configAnonymizer) mac(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.MACAddresses[real]; ok {
		return fake
	}

	var fake string
	for {
		fake = fmt.Sprintf("%04x.%04x.%04x",
			a.fake.UIntBetween(0, 0xFFFF), a.fake.UIntBetween(0, 0xFFFF), a.fake.UIntBetween(0, 0xFFFF))
		if _, taken := a.usedMACs[fake]; !taken {
			break
		}
	}

	a.usedMACs[fake] = struct{}{}
	a.mapping.MACAddresses[real] = fake

	return fake
}

func (a *configAnonymizer) freeText(real string) string {
	if real == "" {
		return real
	}
	if fake, ok := a.mapping.FreeText[real]; ok {
		return fake
	}

	words := len(strings.Fields(real))
	if words < 3 {
		words = 3
	}
	if words > 12 {
		words = 12
	}

	fake := a.fake.Lorem().Sentence(words)
	fake = strings.ToUpper(fake[:1]) + fake[1:]

	a.mapping.FreeText[real] = fake

	return fake
}

func (a *configAnonymizer) redactSecret() {
	a.mapping.RedactedSecrets++
}
