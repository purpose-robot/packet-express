package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/scrapli/scrapligo/driver/options"
	"github.com/scrapli/scrapligo/logging"
	"github.com/scrapli/scrapligo/platform"
	"golang.org/x/term"

	"github.com/purpose-robot/neighbor-explorer/internal/parsing"
)

func main() {
	setDebug := flag.Bool("debug", false, "Set logging level to 'debug'")
	location := flag.String("location", "", "Define prefix for output files")
	mgmtAddr := flag.String("mgmtAddr", "", "Use provided mgmt IP to connect to devices")
	username := flag.String("username", "", "Use provided username to connect to devices")

	flag.Parse()

	if *location == "" || *mgmtAddr == "" || *username == "" {
		log.Fatal("failed to fetch command-line flags; missing flags")
	}

	fmt.Printf("Enter password for %s: ", *username)
	parsedPassword, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()

	if err != nil {
		log.Fatalf("failed to fetch password from command-line; %v", err)
	}
	if len(parsedPassword) == 0 {
		log.Fatal("failed to fetch password from command-line; no password")
	}

	visited := make(map[string]bool)

	logLevel := logging.Info
	if *setDebug {
		logLevel = logging.Debug
	}

	logger, err := logging.NewInstance(
		logging.WithLevel(logLevel), logging.WithLogger(log.Print),
	)
	if err != nil {
		log.Fatalf("failed to initiate new scrapli logging instance; %v", err)
	}

	if err := os.MkdirAll("output", 0755); err != nil {
		log.Fatalf("failed to create folder for configuration snippets; %v", err)
	}

	err = exploreDevice(
		logger, *location, *mgmtAddr, *username, string(parsedPassword), visited,
	)
	if err != nil {
		log.Fatalf("failed to retrieve information from network device %s; %v", *mgmtAddr, err)
	}
}

func strVal(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}

	s, ok := v.(string)
	if !ok {
		return ""
	}

	return s
}

func exploreDevice(logger *logging.Instance, location, mgmtAddr, username, password string, visited map[string]bool) error {
	if visited[mgmtAddr] {
		logger.Infof("host %s already visited", mgmtAddr)
		return nil
	}

	visited[mgmtAddr] = true

	cdpNeighbors, err := fetchDataFromDevice(logger, location, mgmtAddr, username, password)
	if err != nil {
		return err
	}

	containsAny := func(s string, subStrings []string) bool {
		return slices.ContainsFunc(subStrings, func(subString string) bool {
			return strings.Contains(s, subString)
		})
	}

	skippingPlatforms := []string{"AIR", "C91", "C98", "C1", "IE-3000", "Phone", "VG", "ISR", "C8"}

	for _, neighbor := range cdpNeighbors {
		remoteMgmtAddr := strVal(neighbor, "MGMT_ADDRESS")

		if remoteMgmtAddr == "" {
			logger.Infof("skipping discovery for network device %s; Missing IP address", strVal(neighbor, "NEIGHBOR_NAME"))
			continue
		}

		remotePlatform := strVal(neighbor, "PLATFORM")

		if !containsAny(remotePlatform, skippingPlatforms) {
			err = exploreDevice(logger, location, remoteMgmtAddr, username, password, visited)
			if err != nil {
				logger.Infof("failed to retrieve information from network device %s; %v", remoteMgmtAddr, err)
			}
		}
	}

	return nil
}

func fetchDataFromDevice(logger *logging.Instance, location, mgmtAddr, username, password string) ([]map[string]any, error) {
	client, err := platform.NewPlatform(
		"cisco_iosxe",
		mgmtAddr,
		options.WithPort(22),
		options.WithLogger(logger),
		options.WithAuthUsername(username),
		options.WithAuthPassword(password),
		options.WithAuthNoStrictKey(),
		options.WithTransportType("standard"),
		options.WithTimeoutOps(10*time.Second),
		options.WithTimeoutSocket(10*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initiate scrapli cli client; %w", err)
	}

	driver, err := client.GetNetworkDriver()
	if err != nil {
		return nil, fmt.Errorf("failed to fetch scrapli network driver; %w", err)
	}

	if err := driver.Open(); err != nil {
		return nil, fmt.Errorf("failed to initiate connection to network driver; %w", err)
	}
	defer driver.Close()

	responses, err := driver.SendCommandsFromFile("send_commands.txt")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch configuration from network device %s; %w", mgmtAddr, err)
	}
	if responses.Failed != nil {
		logger.Infof("failed to fetch some output from network device; %v", responses.Failed)
	}

	version, err := parsing.Template("cisco_ios_show_version.textfsm", responses.JoinedResult())
	if err != nil {
		return nil, fmt.Errorf("failed to parse version information from network device %s; %w", mgmtAddr, err)
	}

	hostname := mgmtAddr

	if len(version) > 0 {
		hostname, _, _ = strings.Cut(strVal(version[0], "HOSTNAME"), ".")
	}

	filename := filepath.Join("output", strings.ToLower(location+"_"+hostname+".txt"))

	var output strings.Builder

	for _, response := range responses.Responses {
		output.WriteString(response.Input + "\n\n")
		output.WriteString(response.Result + "\n\n")
	}

	if err := os.WriteFile(filename, []byte(output.String()), 0644); err != nil {
		return nil, fmt.Errorf("failed to write scrapli response to %s; %w", filename, err)
	}

	cdpNeighborsDetail, err := parsing.Template("cisco_ios_show_cdp_neighbors_detail.textfsm", responses.JoinedResult())
	if err != nil {
		return nil, fmt.Errorf("failed to parse CDP neighbor detail information from network device %s; %w", mgmtAddr, err)
	}

	return cdpNeighborsDetail, nil
}
