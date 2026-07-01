// Command config-anonymizer rewrites a Cisco IOS XE running-config into a
// version safe to share externally: hostnames, domains, usernames, IPv4/IPv6
// addresses, MAC addresses, BGP AS numbers, VRF names, interface
// descriptions, banners, and SNMP location/contact are replaced with
// consistent fake values. Passwords, secrets, SNMP community strings,
// TACACS+/RADIUS keys, pre-shared keys and certificate/key material are
// redacted outright rather than replaced, since they should never be reused
// or reconstructed.
//
// A mapping file records every fake substitution (but never a redacted
// secret, which is simply dropped) so a Claude-edited copy of the config can
// later be restored to its real values with -deanonymize.
package main

import (
	"flag"
	"log"
	"os"

	"github.com/jaswdr/faker/v2"
)

func main() {
	inputFile := flag.String("input", "running-config.txt", "config file to process")
	outputFile := flag.String("output", "running-config.anon.txt", "file to write the result to")
	mapFile := flag.String("map", "running-config.map.json", "mapping file; written when anonymizing, read when -deanonymize is set")
	deanonymize := flag.Bool("deanonymize", false, "reverse a previous anonymization using the mapping file, instead of anonymizing")
	seed := flag.Int64("seed", 0, "optional fixed seed for reproducible fake values (0 = random)")

	flag.Parse()

	raw, err := os.ReadFile(*inputFile)
	if err != nil {
		log.Fatalf("failed to read input file %s; %v", *inputFile, err)
	}

	if *deanonymize {
		mapping, err := loadMapping(*mapFile)
		if err != nil {
			log.Fatalf("failed to load mapping file %s; %v", *mapFile, err)
		}

		result := mapping.deanonymize(string(raw))

		if err := os.WriteFile(*outputFile, []byte(result), 0644); err != nil {
			log.Fatalf("failed to write de-anonymized config to %s; %v", *outputFile, err)
		}

		log.Printf("de-anonymized %s to %s using %s", *inputFile, *outputFile, *mapFile)
		return
	}

	fake := faker.New()
	if *seed != 0 {
		fake = faker.NewWithSeedInt64(*seed)
	}

	anon := newConfigAnonymizer(fake)
	result := anon.Anonymize(string(raw))

	if err := os.WriteFile(*outputFile, []byte(result), 0644); err != nil {
		log.Fatalf("failed to write anonymized config to %s; %v", *outputFile, err)
	}

	if err := anon.mapping.save(*mapFile); err != nil {
		log.Fatalf("failed to write mapping file %s; %v", *mapFile, err)
	}

	log.Printf(
		"anonymized %s to %s; wrote mapping to %s (%d hostname(s), %d IPv4, %d IPv6, %d secret(s) redacted)",
		*inputFile, *outputFile, *mapFile,
		len(anon.mapping.Hostnames), len(anon.mapping.IPv4), len(anon.mapping.IPv6), anon.mapping.RedactedSecrets,
	)
}
