package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/purpose-robot/neighbor-explorer/internal/parsing"
)

const (
	versionTemplate   = "cisco_ios_show_version.textfsm"
	cdpTemplate       = "cisco_ios_show_cdp_neighbors_detail.textfsm"
	switchTemplate    = "cisco_ios_show_switch_detail.textfsm"
	defaultInputDir   = "output"
	defaultOutputFile = "neighbors_view.json"
)

// switchMemberRow matches a member line of the "show switch" table, e.g.
// "*1   Active   xxxx.xxxx.xxxx   15   V02   Ready". It is used to find where
// the table ends inside the combined device output.
var switchMemberRow = regexp.MustCompile(`^\s*\*?\s*\d+\s+\w+\s+\S+\s+\d+`)

// StackMember describes a single switch within a stacked device.
type StackMember struct {
	ID              int    `json:"id"`
	Role            string `json:"role,omitempty"`
	SerialNumber    string `json:"serial_number"`
	SoftwareVersion string `json:"software_version"`
}

// Device describes a single managed device, either standalone or stacked.
type Device struct {
	Platform        string        `json:"platform"`
	Hostname        string        `json:"hostname"`
	IPAddress       string        `json:"ip_address"`
	StackMembers    []StackMember `json:"stack_members,omitempty"`
	SerialNumber    string        `json:"serial_number,omitempty"`
	SoftwareVersion string        `json:"software_version,omitempty"`
}

// Neighbor describes one CDP adjacency as seen from the local device.
type Neighbor struct {
	LocalHostname   string `json:"local_hostname"`
	LocalInterface  string `json:"local_interface"`
	LocalIPAddress  string `json:"local_ip_address"`
	RemotePlatform  string `json:"remote_platform"`
	RemoteHostname  string `json:"remote_hostname"`
	RemoteInterface string `json:"remote_interface"`
	RemoteIPAddress string `json:"remote_ip_address"`
}

// LocationView groups all devices and adjacencies discovered for a location.
type LocationView struct {
	Devices   []Device   `json:"devices"`
	Neighbors []Neighbor `json:"neighbors"`
}

func strVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}

	switch t := v.(type) {
	case string:
		return t
	case []string:
		if len(t) > 0 {
			return t[0]
		}
	}

	return ""
}

func intVal(m map[string]any, key string) int {
	n, err := strconv.Atoi(strVal(m, key))
	if err != nil {
		return 0
	}

	return n
}

func listVal(m map[string]any, key string) []string {
	v, ok := m[key]
	if !ok {
		return nil
	}

	switch t := v.(type) {
	case []string:
		return t
	case string:
		if t != "" {
			return []string{t}
		}
	}

	return nil
}

func main() {
	inputDir := flag.String("input", defaultInputDir, "directory holding captured device output")
	outputFile := flag.String("output", defaultOutputFile, "file to write the neighbors view to")

	flag.Parse()

	entries, err := os.ReadDir(*inputDir)
	if err != nil {
		log.Fatalf("failed to read input directory %s; %v", *inputDir, err)
	}

	view := make(map[string]*LocationView)

	// ipByHostname maps a device hostname to the management address its
	// neighbors advertise for it. A device does not print its own management
	// address in "show version", so we recover it from the CDP detail of the
	// devices that see it.
	ipByHostname := make(map[string]string)

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".txt") {
			continue
		}

		path := filepath.Join(*inputDir, entry.Name())

		content, err := os.ReadFile(path)
		if err != nil {
			log.Printf("skipping %s; failed to read file; %v", path, err)
			continue
		}

		location := locationFromName(entry.Name())

		device, neighbors, err := parseDevice(string(content))
		if err != nil {
			log.Printf("skipping %s; %v", path, err)
			continue
		}

		lv, ok := view[location]
		if !ok {
			lv = &LocationView{}
			view[location] = lv
		}

		lv.Devices = append(lv.Devices, device)
		lv.Neighbors = append(lv.Neighbors, neighbors...)

		for _, neighbor := range neighbors {
			if neighbor.RemoteHostname != "" && neighbor.RemoteIPAddress != "" {
				ipByHostname[neighbor.RemoteHostname] = neighbor.RemoteIPAddress
			}
		}
	}

	// Backfill the management addresses now that every file has contributed
	// its view of the topology.
	for _, lv := range view {
		for i := range lv.Devices {
			if lv.Devices[i].IPAddress == "" {
				lv.Devices[i].IPAddress = ipByHostname[lv.Devices[i].Hostname]
			}
		}
		for i := range lv.Neighbors {
			if lv.Neighbors[i].LocalIPAddress == "" {
				lv.Neighbors[i].LocalIPAddress = ipByHostname[lv.Neighbors[i].LocalHostname]
			}
		}
	}

	rendered, err := json.MarshalIndent(view, "", "  ")
	if err != nil {
		log.Fatalf("failed to marshal neighbors view; %v", err)
	}

	if err := os.WriteFile(*outputFile, append(rendered, '\n'), 0644); err != nil {
		log.Fatalf("failed to write neighbors view to %s; %v", *outputFile, err)
	}

	log.Printf("wrote %d location(s) to %s", len(view), *outputFile)
}

// locationFromName recovers the location prefix from a "<location>_<hostname>.txt"
// filename produced by the explorer.
func locationFromName(name string) string {
	base := strings.TrimSuffix(name, filepath.Ext(name))

	if idx := strings.Index(base, "_"); idx >= 0 {
		return base[:idx]
	}

	return base
}

// parseDevice turns the combined "show version" and "show cdp neighbors detail"
// output of a single device into a Device and its neighbor adjacencies.
func parseDevice(content string) (Device, []Neighbor, error) {
	versions, err := parsing.Template(versionTemplate, content)
	if err != nil {
		return Device{}, nil, err
	}

	cdpEntries, err := parsing.Template(cdpTemplate, content)
	if err != nil {
		return Device{}, nil, err
	}

	device := buildDevice(versions, parseSwitches(content))

	neighbors := make([]Neighbor, 0, len(cdpEntries))
	for _, entry := range cdpEntries {
		neighbors = append(neighbors, Neighbor{
			LocalHostname:   device.Hostname,
			LocalInterface:  strVal(entry, "LOCAL_INTERFACE"),
			RemotePlatform:  strVal(entry, "PLATFORM"),
			RemoteHostname:  strVal(entry, "NEIGHBOR_NAME"),
			RemoteInterface: strVal(entry, "NEIGHBOR_INTERFACE"),
			RemoteIPAddress: strVal(entry, "MGMT_ADDRESS"),
		})
	}

	return device, neighbors, nil
}

// buildDevice assembles a Device from the parsed "show version" record(s). When
// the device reports more than one serial number it is treated as a stack, and
// the parsed "show switch" records (when present) supply the elected member id
// and role. Stacks and StackWise Virtual cores look identical here: both report
// their members through "show switch".
func buildDevice(versions, switches []map[string]any) Device {
	device := Device{}
	if len(versions) == 0 {
		return device
	}

	record := versions[0]

	device.Hostname = strVal(record, "HOSTNAME")
	device.Platform = strVal(record, "HARDWARE")

	version := strVal(record, "VERSION")
	serials := listVal(record, "SERIAL")

	if !isMultiMember(switches, serials) {
		// A standalone switch (or a StackWise Virtual member captured on its
		// own) is rendered with flat serial/version fields rather than a stack.
		if len(serials) > 0 {
			device.SerialNumber = serials[0]
		}
		device.SoftwareVersion = version

		return device
	}

	count := len(serials)
	if len(switches) > count {
		count = len(switches)
	}

	for i := 0; i < count; i++ {
		member := StackMember{
			ID:              i + 1,
			Role:            stackRole(i),
			SoftwareVersion: version,
		}

		if i < len(serials) {
			member.SerialNumber = serials[i]
		}

		// "show version" lists serials in member order, so the i-th
		// "show switch" record describes the same member.
		if i < len(switches) {
			if id := intVal(switches[i], "SWITCH"); id > 0 {
				member.ID = id
			}
			if role := strVal(switches[i], "ROLE"); role != "" {
				member.Role = strings.ToLower(role)
			}
		}

		device.StackMembers = append(device.StackMembers, member)
	}

	return device
}

// isMultiMember reports whether a device is a stack or StackWise Virtual pair.
// The "show switch" table is authoritative when present: a single member row
// means a standalone switch even if "show version" repeats the serial in a
// per-switch block. Without that table, distinct serial numbers are the only
// signal available.
func isMultiMember(switches []map[string]any, serials []string) bool {
	if len(switches) > 0 {
		return len(switches) > 1
	}

	distinct := make(map[string]struct{}, len(serials))
	for _, serial := range serials {
		if serial != "" {
			distinct[serial] = struct{}{}
		}
	}

	return len(distinct) > 1
}

// parseSwitches parses the "show switch" table out of a device's combined
// output. The template raises on unrecognised lines, so the table is first
// sliced out of the surrounding "show version"/CDP text. A missing or
// malformed table is non-fatal: callers fall back to positional roles.
func parseSwitches(content string) []map[string]any {
	section := switchSection(content)
	if section == "" {
		return nil
	}

	switches, err := parsing.Template(switchTemplate, section)
	if err != nil {
		log.Printf("failed to parse switch inventory; %v", err)
		return nil
	}

	return switches
}

// switchSection returns the lines of the "show switch" table, from its header
// down to the last member row, or "" when no table is present.
func switchSection(content string) string {
	lines := strings.Split(content, "\n")

	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "Switch/Stack Mac Address") {
			start = i
			break
		}
	}
	if start < 0 {
		return ""
	}

	end := len(lines)
	seenSeparator := false
	for i := start; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])

		if trimmed == "" {
			continue
		}

		if !seenSeparator {
			// Header lines precede the dashed separator that opens the table.
			if strings.HasPrefix(trimmed, "---") {
				seenSeparator = true
			}
			continue
		}

		// Past the separator only member rows belong to the table. Anything
		// else (including a CDP entry's dashed line) is the next command's
		// output and ends the section.
		if !switchMemberRow.MatchString(lines[i]) {
			end = i
			break
		}
	}

	return strings.Join(lines[start:end], "\n")
}

// stackRole infers a stack member's role from its position. It is the fallback
// when "show switch" output is unavailable and the elected role is unknown.
func stackRole(index int) string {
	switch index {
	case 0:
		return "active"
	case 1:
		return "standby"
	default:
		return "member"
	}
}
