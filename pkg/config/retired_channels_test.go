package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A config an earlier version wrote holds entries of the removed chat apps,
// with their secrets in .security.yml. It loads without them, and saving it
// writes neither the entries nor their secrets back.
func TestRetiredChannelsLoadAndAreDropped(t *testing.T) {
	path := writeConfigFile(t, `{
  "channel_list": {
    "telegram": {"enabled": false, "settings": {"use_markdown_v2": false}},
    "mybot": {"enabled": true, "type": "discord", "settings": {}},
    "slack": {"enabled": false, "type": "slack", "settings": {}}
  }
}`)
	security := filepath.Join(filepath.Dir(path), SecurityConfigFile)
	if err := os.WriteFile(security, []byte(`channel_list:
  telegram:
    settings:
      token: tg-secret
  mybot:
    settings:
      token: discord-secret
  slack:
    settings:
      bot_token: xoxb-kept
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	for _, name := range []string{"telegram", "mybot"} {
		if cfg.Channels[name] != nil {
			t.Errorf("channel %q survived the load", name)
		}
	}
	decoded, err := cfg.Channels["slack"].GetDecoded()
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.(*SlackSettings).BotToken.String(); got != "xoxb-kept" {
		t.Errorf("slack bot token = %q, want the one in .security.yml", got)
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	for _, file := range []string{path, security} {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, gone := range []string{"telegram", "mybot", "tg-secret", "discord-secret"} {
			if strings.Contains(string(data), gone) {
				t.Errorf("%s still holds %q:\n%s", filepath.Base(file), gone, data)
			}
		}
	}
}

// The web UI saves a config decoded from its JSON, then fills the secrets
// from .security.yml. Secrets of a channel the config no longer has stay out
// of config.json.
func TestSecretsOfAMissingChannelAreNotCopied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	security := filepath.Join(filepath.Dir(path), SecurityConfigFile)
	if err := os.WriteFile(security, []byte(`channel_list:
  telegram:
    settings:
      token: tg-secret
`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := DefaultConfig()
	if err := cfg.SecurityCopyFrom(path); err != nil {
		t.Fatalf("SecurityCopyFrom: %v", err)
	}
	if cfg.Channels["telegram"] != nil {
		t.Fatal("the secrets of a missing channel made a channel")
	}
	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tg-secret") {
		t.Fatalf("config.json holds a secret:\n%s", data)
	}
}
