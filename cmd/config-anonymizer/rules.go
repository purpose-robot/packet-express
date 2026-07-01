package main

import "regexp"

var (
	reHostname   = regexp.MustCompile(`^(\s*hostname\s+)(\S+)\s*$`)
	reDomainName = regexp.MustCompile(`^(\s*ip domain[- ]name\s+)(\S+)\s*$`)
	reUsername   = regexp.MustCompile(`^(\s*username\s+)(\S+)(.*)$`)

	reEnableSecret = regexp.MustCompile(`^(\s*enable (?:secret|password)\s+)((?:\d{1,2}\s+)?)(\S+)\s*$`)
	reLinePassword = regexp.MustCompile(`^(\s*password\s+)((?:\d{1,2}\s+)?)(\S+)\s*$`)
	reKeyString    = regexp.MustCompile(`^(\s*key-string\s+)(\S+)\s*$`)
	rePreSharedKey = regexp.MustCompile(`^(\s*pre-shared-key\s+)((?:\d{1,2}\s+)?)(\S+)\s*$`)
	reNTPAuthKey   = regexp.MustCompile(`^(\s*ntp authentication-key\s+\d+\s+md5\s+)(\S+)\s*$`)
	reServerKey    = regexp.MustCompile(`^(\s*key\s+)((?:\d{1,2}\s+)?)(\S+)\s*$`)

	reSNMPCommunity = regexp.MustCompile(`^(\s*snmp-server community\s+)(\S+)(.*)$`)
	reSNMPHostV3    = regexp.MustCompile(`^(\s*snmp-server host\s+\S+.*\bversion\s+3\s+\S+\s+)(\S+)(.*)$`)
	reSNMPHostV12   = regexp.MustCompile(`^(\s*snmp-server host\s+\S+.*\bversion\s+(?:1|2c)\s+)(\S+)(.*)$`)
	reSNMPHostPlain = regexp.MustCompile(`^(\s*snmp-server host\s+\S+\s+)(\S+)(.*)$`)
	reSNMPLocation  = regexp.MustCompile(`^(\s*snmp-server location\s+)(.*)$`)
	reSNMPContact   = regexp.MustCompile(`^(\s*snmp-server contact\s+)(.*)$`)

	reBGPNeighborDesc = regexp.MustCompile(`^(\s*neighbor\s+\S+\s+description\s+)(.*)$`)
	reDescription     = regexp.MustCompile(`^(\s*description\s+)(.*)$`)

	reRouterBGP = regexp.MustCompile(`^(\s*router bgp\s+)(\d+)\s*$`)
	reRemoteAS  = regexp.MustCompile(`^(\s*neighbor\s+\S+\s+remote-as\s+)(\d+)(.*)$`)
	reLocalAS   = regexp.MustCompile(`^(\s*(?:neighbor\s+\S+\s+)?local-as\s+)(\d+)(.*)$`)

	reVRFDefinition = regexp.MustCompile(`^(\s*vrf definition\s+)(\S+)\s*$`)
	reVRFForwarding = regexp.MustCompile(`^(\s*vrf forwarding\s+)(\S+)\s*$`)
	reIPVRF         = regexp.MustCompile(`^(\s*ip vrf\s+)(\S+)\s*$`)

	reSecretTail  = regexp.MustCompile(`(?i)\b(secret|password)\b(\s+\d{1,2})?\s+\S+`)
	reNumericOnly = regexp.MustCompile(`^\d+$`)
)

// processLine anonymizes a single config line against every known Cisco
// IOS XE construct except IP/IPv6/MAC addresses, which are substituted in a
// separate pass over the whole file so every occurrence -- no matter which
// command embeds it -- is treated consistently.
func (a *configAnonymizer) processLine(line string) string {
	switch {
	case reHostname.MatchString(line):
		m := reHostname.FindStringSubmatch(line)
		return m[1] + a.hostname(m[2])

	case reDomainName.MatchString(line):
		m := reDomainName.FindStringSubmatch(line)
		return m[1] + a.domain(m[2])

	case reUsername.MatchString(line):
		m := reUsername.FindStringSubmatch(line)
		prefix, name, tail := m[1], m[2], m[3]
		if redacted, ok := redactSecretTail(tail); ok {
			a.redactSecret()
			tail = redacted
		}
		return prefix + a.username(name) + tail

	case reEnableSecret.MatchString(line):
		m := reEnableSecret.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + m[2] + "<redacted>"

	case reKeyString.MatchString(line):
		m := reKeyString.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>"

	case rePreSharedKey.MatchString(line):
		m := rePreSharedKey.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + m[2] + "<redacted>"

	case reNTPAuthKey.MatchString(line):
		m := reNTPAuthKey.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>"

	case reSNMPCommunity.MatchString(line):
		m := reSNMPCommunity.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>" + m[3]

	case reSNMPHostV3.MatchString(line):
		m := reSNMPHostV3.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>" + m[3]

	case reSNMPHostV12.MatchString(line):
		m := reSNMPHostV12.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>" + m[3]

	case reSNMPHostPlain.MatchString(line):
		m := reSNMPHostPlain.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + "<redacted>" + m[3]

	case reSNMPLocation.MatchString(line):
		m := reSNMPLocation.FindStringSubmatch(line)
		return m[1] + a.freeText(m[2])

	case reSNMPContact.MatchString(line):
		m := reSNMPContact.FindStringSubmatch(line)
		return m[1] + a.freeText(m[2])

	case reBGPNeighborDesc.MatchString(line):
		m := reBGPNeighborDesc.FindStringSubmatch(line)
		return m[1] + a.freeText(m[2])

	case reDescription.MatchString(line):
		m := reDescription.FindStringSubmatch(line)
		return m[1] + a.freeText(m[2])

	case reRouterBGP.MatchString(line):
		m := reRouterBGP.FindStringSubmatch(line)
		return m[1] + a.asn(m[2])

	case reRemoteAS.MatchString(line):
		m := reRemoteAS.FindStringSubmatch(line)
		return m[1] + a.asn(m[2]) + m[3]

	case reLocalAS.MatchString(line):
		m := reLocalAS.FindStringSubmatch(line)
		return m[1] + a.asn(m[2]) + m[3]

	case reVRFDefinition.MatchString(line):
		m := reVRFDefinition.FindStringSubmatch(line)
		return m[1] + a.vrf(m[2])

	case reVRFForwarding.MatchString(line):
		m := reVRFForwarding.FindStringSubmatch(line)
		return m[1] + a.vrf(m[2])

	case reIPVRF.MatchString(line):
		m := reIPVRF.FindStringSubmatch(line)
		return m[1] + a.vrf(m[2])

	case reLinePassword.MatchString(line):
		m := reLinePassword.FindStringSubmatch(line)
		a.redactSecret()
		return m[1] + m[2] + "<redacted>"

	case reServerKey.MatchString(line):
		m := reServerKey.FindStringSubmatch(line)
		typeTok, value := m[2], m[3]
		if typeTok == "" && reNumericOnly.MatchString(value) {
			// A bare "key <n>" with no type marker and a purely numeric
			// value is a key-chain key ID, not a secret -- leave it alone.
			return line
		}
		a.redactSecret()
		return m[1] + typeTok + "<redacted>"

	default:
		return line
	}
}

// redactSecretTail finds a trailing "secret"/"password" clause (as seen on a
// username line) and blanks the value that follows it, keeping the keyword
// and any Cisco encryption-type digit intact.
func redactSecretTail(tail string) (string, bool) {
	if !reSecretTail.MatchString(tail) {
		return tail, false
	}

	return reSecretTail.ReplaceAllString(tail, "$1$2 <redacted>"), true
}
