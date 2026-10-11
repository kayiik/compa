// Package identity provides unified user identity utilities for Compa.
// It introduces a canonical "platform:id" format and the matching logic for
// allow-list entries.
package identity

import (
	"strings"

	"github.com/kayiik/compa/pkg/bus"
)

// BuildCanonicalID constructs a canonical "platform:id" identifier.
// Both platform and platformID are lowercased and trimmed.
func BuildCanonicalID(platform, platformID string) string {
	p := strings.ToLower(strings.TrimSpace(platform))
	id := strings.TrimSpace(platformID)
	if p == "" || id == "" {
		return ""
	}
	return p + ":" + id
}

// ParseCanonicalID splits a canonical ID ("platform:id") into its parts.
// Returns ok=false if the input does not contain a colon separator.
func ParseCanonicalID(canonical string) (platform, id string, ok bool) {
	canonical = strings.TrimSpace(canonical)
	idx := strings.Index(canonical, ":")
	if idx <= 0 || idx == len(canonical)-1 {
		return "", "", false
	}
	return canonical[:idx], canonical[idx+1:], true
}

// knownPlatforms are the channel types whose senders carry canonical IDs. Only
// these names make an allow-list entry a "platform:id" entry: other text
// before a colon belongs to the ID itself.
var knownPlatforms = map[string]struct{}{
	"web": {}, "web_client": {}, "slack": {}, "whatsapp": {},
	"whatsapp_native": {}, "teams_webhook": {}, "slack_webhook": {},
}

// MatchAllowed checks whether the given sender matches a single allow-list entry:
//
//   - "123456"              → matches sender.PlatformID
//   - "@alice"              → matches sender.Username
//   - "slack:U123456"       → exact match on sender.CanonicalID
//   - "slack:@alice"        → matches sender.Username on that platform
//
// Only a known platform name (or the sender's own platform) before the first
// colon makes an entry canonical.
func MatchAllowed(sender bus.SenderInfo, allowed string) bool {
	allowed = strings.TrimSpace(allowed)
	if allowed == "" {
		return false
	}

	// Canonical match: "platform:id" format
	if platform, id, ok := ParseCanonicalID(allowed); ok && isPlatform(platform, sender.Platform) {
		candidate := BuildCanonicalID(platform, id)
		if candidate != "" && sender.CanonicalID != "" && strings.EqualFold(sender.CanonicalID, candidate) {
			return true
		}
		if !strings.EqualFold(platform, sender.Platform) {
			return false
		}
		if name, isUsername := strings.CutPrefix(id, "@"); isUsername && name != "" && usernameMatches(sender, name) {
			return true
		}
		// If sender has no canonical ID, try matching platform + platformID
		return sender.CanonicalID == "" && platformIDMatches(sender, id)
	}

	// Keep track of explicit username format
	name, isAtUsername := strings.CutPrefix(allowed, "@")

	// Match against PlatformID: the entry as written, or without its "@".
	if sender.PlatformID != "" && (platformIDMatches(sender, allowed) || platformIDMatches(sender, name)) {
		return true
	}

	// Match against Username only when explicitly requested via "@username"
	return isAtUsername && name != "" && usernameMatches(sender, name)
}

// isPlatform reports whether prefix names a platform: a known channel type,
// or the sender's own platform.
func isPlatform(prefix, senderPlatform string) bool {
	p := strings.ToLower(strings.TrimSpace(prefix))
	if p == "" || isNumeric(p) {
		return false
	}
	if _, ok := knownPlatforms[p]; ok {
		return true
	}
	return senderPlatform != "" && strings.EqualFold(p, strings.TrimSpace(senderPlatform))
}

// platformIDMatches compares id with the sender's platform ID.
func platformIDMatches(sender bus.SenderInfo, id string) bool {
	return sender.PlatformID != "" && id != "" && sender.PlatformID == id
}

// usernameMatches compares an "@name" entry, without its "@", with the
// sender's username.
func usernameMatches(sender bus.SenderInfo, name string) bool {
	username := strings.TrimPrefix(sender.Username, "@")
	return username != "" && username == name
}

// isNumeric returns true if s consists entirely of digits, allowing for an optional leading minus sign.
func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	start := 0
	if s[0] == '-' && len(s) > 1 {
		start = 1
	}
	for i := start; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}
