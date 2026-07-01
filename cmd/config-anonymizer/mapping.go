package main

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
)

// Mapping records every real value replaced during anonymization, keyed by
// category, so the transformation can later be reversed with -deanonymize.
// Redacted secrets are intentionally never recorded here: they are dropped,
// not mapped, so this file cannot be used to recover credentials.
type Mapping struct {
	Hostnames       map[string]string `json:"hostnames,omitempty"`
	Domains         map[string]string `json:"domains,omitempty"`
	Usernames       map[string]string `json:"usernames,omitempty"`
	IPv4            map[string]string `json:"ipv4,omitempty"`
	IPv6            map[string]string `json:"ipv6,omitempty"`
	MACAddresses    map[string]string `json:"mac_addresses,omitempty"`
	ASNs            map[string]string `json:"asns,omitempty"`
	VRFs            map[string]string `json:"vrfs,omitempty"`
	FreeText        map[string]string `json:"free_text,omitempty"`
	RedactedSecrets int               `json:"redacted_secrets"`
}

func newMapping() *Mapping {
	return &Mapping{
		Hostnames:    make(map[string]string),
		Domains:      make(map[string]string),
		Usernames:    make(map[string]string),
		IPv4:         make(map[string]string),
		IPv6:         make(map[string]string),
		MACAddresses: make(map[string]string),
		ASNs:         make(map[string]string),
		VRFs:         make(map[string]string),
		FreeText:     make(map[string]string),
	}
}

func loadMapping(path string) (*Mapping, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read mapping file %s; %w", path, err)
	}

	m := newMapping()
	if err := json.Unmarshal(raw, m); err != nil {
		return nil, fmt.Errorf("failed to parse mapping file %s; %w", path, err)
	}

	return m, nil
}

func (m *Mapping) save(path string) error {
	rendered, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mapping; %w", err)
	}

	if err := os.WriteFile(path, append(rendered, '\n'), 0600); err != nil {
		return fmt.Errorf("failed to write mapping file %s; %w", path, err)
	}

	return nil
}

// deanonymize restores every mapped fake value found in text back to its
// real counterpart. Redacted secrets cannot be restored: they were dropped,
// not mapped, when the config was anonymized.
func (m *Mapping) deanonymize(text string) string {
	type pair struct{ fake, real string }

	var pairs []pair

	add := func(category map[string]string) {
		for real, fake := range category {
			if real == "" || fake == "" {
				continue
			}
			pairs = append(pairs, pair{fake, real})
		}
	}

	add(m.Hostnames)
	add(m.Domains)
	add(m.Usernames)
	add(m.IPv4)
	add(m.IPv6)
	add(m.MACAddresses)
	add(m.ASNs)
	add(m.VRFs)
	add(m.FreeText)

	// Longest fake value first, so a fake value that happens to be a
	// substring of another (e.g. "10.0.0.1" inside "10.0.0.11") cannot be
	// substituted before the longer match containing it is handled.
	sort.Slice(pairs, func(i, j int) bool { return len(pairs[i].fake) > len(pairs[j].fake) })

	for _, p := range pairs {
		// A plain \b...\b can never match a value that starts or ends on a
		// non-word character (every free-text value ends in "."): \b needs a
		// word/non-word transition, and "." followed by a space or newline is
		// non-word on both sides. Capturing one character of context (or
		// start/end of string) on each side works regardless of the fake
		// value's own edge characters.
		re := regexp.MustCompile(`(^|[^0-9A-Za-z_])` + regexp.QuoteMeta(p.fake) + `($|[^0-9A-Za-z_])`)
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			sub := re.FindStringSubmatch(m)
			return sub[1] + p.real + sub[2]
		})
	}

	return text
}
