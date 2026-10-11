package identity

import (
	"testing"

	"github.com/kayiik/compa/pkg/bus"
)

func TestBuildCanonicalID(t *testing.T) {
	tests := []struct {
		platform   string
		platformID string
		want       string
	}{
		{"telegram", "123456", "telegram:123456"},
		{"Discord", "98765432", "discord:98765432"},
		{"SLACK", "U123ABC", "slack:U123ABC"},
		{"", "123", ""},
		{"telegram", "", ""},
		{"  telegram  ", "  123  ", "telegram:123"},
	}

	for _, tt := range tests {
		got := BuildCanonicalID(tt.platform, tt.platformID)
		if got != tt.want {
			t.Errorf("BuildCanonicalID(%q, %q) = %q, want %q",
				tt.platform, tt.platformID, got, tt.want)
		}
	}
}

func TestParseCanonicalID(t *testing.T) {
	tests := []struct {
		input        string
		wantPlatform string
		wantID       string
		wantOk       bool
	}{
		{"telegram:123456", "telegram", "123456", true},
		{"discord:98765432", "discord", "98765432", true},
		{"slack:U123ABC", "slack", "U123ABC", true},
		{"nocolon", "", "", false},
		{"", "", "", false},
		{":missing", "", "", false},
		{"missing:", "", "", false},
	}

	for _, tt := range tests {
		platform, id, ok := ParseCanonicalID(tt.input)
		if ok != tt.wantOk || platform != tt.wantPlatform || id != tt.wantID {
			t.Errorf("ParseCanonicalID(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tt.input, platform, id, ok,
				tt.wantPlatform, tt.wantID, tt.wantOk)
		}
	}
}

func TestMatchAllowed(t *testing.T) {
	whatsappSender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  "123456",
		CanonicalID: "whatsapp:123456",
		Username:    "alice",
		DisplayName: "Alice Smith",
	}

	slackSender := bus.SenderInfo{
		Platform:    "slack",
		PlatformID:  "98765432",
		CanonicalID: "slack:98765432",
		Username:    "bob",
		DisplayName: "bob#1234",
	}

	noCanonicalSender := bus.SenderInfo{
		Platform:   "whatsapp",
		PlatformID: "999",
		Username:   "carol",
	}

	tests := []struct {
		name    string
		sender  bus.SenderInfo
		allowed string
		want    bool
	}{
		// Pure numeric ID matching
		{
			name:    "numeric ID matches PlatformID",
			sender:  whatsappSender,
			allowed: "123456",
			want:    true,
		},
		{
			name:    "numeric ID does not match",
			sender:  whatsappSender,
			allowed: "654321",
			want:    false,
		},
		{
			name: "negative numeric ID matches PlatformID",
			sender: bus.SenderInfo{
				Platform:   "whatsapp",
				PlatformID: "-1001234567890",
			},
			allowed: "-1001234567890",
			want:    true,
		},
		// Username matching
		{
			name:    "@username matches Username",
			sender:  whatsappSender,
			allowed: "@alice",
			want:    true,
		},
		{
			name: "plain entry does not match username",
			sender: bus.SenderInfo{
				Platform:   "slack",
				PlatformID: "999999",
				Username:   "123456",
			},
			allowed: "123456",
			want:    false,
		},
		{
			name:    "@username does not match",
			sender:  whatsappSender,
			allowed: "@bob",
			want:    false,
		},
		// "id|username" is not an allow-list format
		{
			name:    "id|username entry is not split",
			sender:  whatsappSender,
			allowed: "123456|alice",
			want:    false,
		},
		// Canonical format "platform:id"
		{
			name:    "canonical matches exactly",
			sender:  whatsappSender,
			allowed: "whatsapp:123456",
			want:    true,
		},
		{
			name:    "canonical case-insensitive platform",
			sender:  whatsappSender,
			allowed: "WhatsApp:123456",
			want:    true,
		},
		{
			name:    "canonical wrong platform",
			sender:  whatsappSender,
			allowed: "slack:123456",
			want:    false,
		},
		{
			name:    "canonical wrong ID",
			sender:  whatsappSender,
			allowed: "whatsapp:654321",
			want:    false,
		},
		// Cross-platform canonical
		{
			name:    "slack canonical match",
			sender:  slackSender,
			allowed: "slack:98765432",
			want:    true,
		},
		{
			name:    "whatsapp canonical does not match slack sender",
			sender:  slackSender,
			allowed: "whatsapp:98765432",
			want:    false,
		},
		// Sender without canonical ID
		{
			name:    "canonical match falls back to platform+platformID",
			sender:  noCanonicalSender,
			allowed: "whatsapp:999",
			want:    true,
		},
		{
			name:    "platform mismatch on fallback",
			sender:  noCanonicalSender,
			allowed: "slack:999",
			want:    false,
		},
		// Empty allowed string
		{
			name:    "empty allowed never matches",
			sender:  whatsappSender,
			allowed: "",
			want:    false,
		},
		// Whitespace handling
		{
			name:    "trimmed allowed matches",
			sender:  whatsappSender,
			allowed: "  123456  ",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchAllowed(tt.sender, tt.allowed)
			if got != tt.want {
				t.Errorf("MatchAllowed(%+v, %q) = %v, want %v",
					tt.sender, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestMatchAllowedPlatformSpecificEntries(t *testing.T) {
	matrixSender := bus.SenderInfo{
		Platform:    "matrix",
		PlatformID:  "@alice:matrix.org",
		CanonicalID: "matrix:@alice:matrix.org",
		Username:    "@alice:matrix.org",
	}
	whatsappSender := bus.SenderInfo{
		Platform:    "whatsapp",
		PlatformID:  "123456",
		CanonicalID: "whatsapp:123456",
		Username:    "Alice_Bot",
	}
	slackSender := bus.SenderInfo{
		Platform:    "slack",
		PlatformID:  "U123",
		CanonicalID: "slack:U123",
		Username:    "Carol",
	}
	ircSender := bus.SenderInfo{
		Platform:    "irc",
		PlatformID:  "nick:with:colons",
		CanonicalID: "irc:nick:with:colons",
	}

	tests := []struct {
		name    string
		sender  bus.SenderInfo
		allowed string
		want    bool
	}{
		// Matrix user IDs contain a colon but are not "platform:id".
		{"matrix user ID", matrixSender, "@alice:matrix.org", true},
		{"matrix canonical user ID", matrixSender, "matrix:@alice:matrix.org", true},
		{"matrix other user", matrixSender, "@alice:evil.org", false},
		{"matrix user ID is case-sensitive", matrixSender, "@Alice:matrix.org", false},
		// Only known channel names are platform prefixes.
		{"unknown prefix is part of the ID", ircSender, "nick:with:colons", true},
		{"unknown prefix does not match canonical form", whatsappSender, "whatsappx:123456", false},
		{"known platform still canonical", whatsappSender, "whatsapp:123456", true},
		{"other known platform does not match", whatsappSender, "slack:123456", false},
		{"numeric prefix is an ID", bus.SenderInfo{Platform: "x", PlatformID: "1:2"}, "1:2", true},
		// Usernames compare exactly.
		{"username keeps case", whatsappSender, "@alice_bot", false},
		{"username exact", whatsappSender, "@Alice_Bot", true},
		{"slack username keeps case", slackSender, "@carol", false},
		{"slack username exact", slackSender, "@Carol", true},
		{"platform-scoped username", slackSender, "slack:@Carol", true},
		{"platform-scoped username on other platform", slackSender, "whatsapp:@Carol", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MatchAllowed(tt.sender, tt.allowed); got != tt.want {
				t.Errorf("MatchAllowed(%+v, %q) = %v, want %v", tt.sender, tt.allowed, got, tt.want)
			}
		})
	}
}

func TestIsNumeric(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"123456", true},
		{"0", true},
		{"", false},
		{"abc", false},
		{"12a34", false},
		{"telegram", false},
		{"-1001234567890", true},
		{"-", false},
		{"-12a34", false},
	}

	for _, tt := range tests {
		got := isNumeric(tt.input)
		if got != tt.want {
			t.Errorf("isNumeric(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
