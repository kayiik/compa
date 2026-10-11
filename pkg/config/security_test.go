// Compa - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Compa contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/caarlos0/env/v11"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestSecurityConfig(t *testing.T) {
	t.Run("LoadNonExistent", func(t *testing.T) {
		sec := &Config{Channels: make(ChannelsConfig)}
		err := loadSecurityConfig(sec, "/nonexistent/.security.yml")
		require.NoError(t, err)
		assert.NotNil(t, sec)
		assert.NotNil(t, sec.Channels)
		assert.NotNil(t, sec.Tools.Web)
		assert.NotNil(t, sec.Tools.Skills)
	})
}

func TestSecurityPath(t *testing.T) {
	tests := []struct {
		name      string
		configDir string
		want      string
	}{
		// The expectations were POSIX literals, so they could only ever match
		// on POSIX -- securityPath uses filepath.Join, which is
		// separator-correct for the host. Build them the same way it does.
		{
			name:      "standard path",
			configDir: filepath.Join("/home/user/.compa", "config.json"),
			want:      filepath.Join("/home/user/.compa", SecurityConfigFile),
		},
		{
			name:      "nested path",
			configDir: filepath.Join("/path/to/config", "myconfig.json"),
			want:      filepath.Join("/path/to/config", SecurityConfigFile),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := securityPath(tt.configDir)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSaveAndLoadSecurityConfig(t *testing.T) {
	t.Run("test for securestring", func(t *testing.T) {
		type testStruct struct {
			Secret SecureString `json:"secret,omitzero" yaml:"secret,omitempty" env:"TEST_SECURE_STRING"`
		}
		s := testStruct{Secret: *NewSecureString("test")}
		out, err := yaml.Marshal(s) // 直接对 SecureString 进行序列化
		require.NoError(t, err)
		t.Logf("output: %v", string(out))
		assert.Equal(t, "secret: test\n", string(out))
		out, err = json.Marshal(s)
		require.NoError(t, err)
		t.Logf("output: %v", string(out))
		assert.Equal(t, `{"secret":"[NOT_HERE]"}`, string(out))

		// The placeholder reads back as unset.
		var back testStruct
		require.NoError(t, json.Unmarshal(out, &back))
		assert.True(t, back.Secret.IsZero())
		assert.Equal(t, "", back.Secret.String())
		out, err = json.Marshal(back)
		require.NoError(t, err)
		assert.Equal(t, "{}", string(out))
	})
	tmpDir := t.TempDir()
	secPath := filepath.Join(tmpDir, SecurityConfigFile)

	original := &Config{
		Tools: ToolsConfig{
			Web: WebToolsConfig{
				Brave: BraveConfig{
					Enabled: true,
					APIKeys: SecureStrings{NewSecureString("brave_key")},
				},
				Tavily: TavilyConfig{
					Enabled: true,
					APIKeys: SecureStrings{NewSecureString("key1"), NewSecureString("key2")},
				},
			},
		},
		Channels: func() ChannelsConfig {
			chs := make(ChannelsConfig)
			type def struct {
				name string
				raw  string // raw JSON with actual secure values (bypasses SecureString.MarshalJSON)
			}
			for _, d := range []def{
				{"slack", `{"enabled":true,"settings":{"bot_token":"slack_bot_token","app_token":"slack_app_token"}}`},
				{"web", `{"enabled":true,"settings":{"token":"web_token","ping_interval":30}}`},
				{"web_client", `{"enabled":true,"settings":{"token":"web_client_token"}}`},
				{
					"teams_webhook",
					`{"enabled":true,"settings":{"webhooks":{"ops":{"title":"Ops","webhook_url":"teams_webhook_url"}}}}`,
				},
			} {
				bc := &Channel{}
				json.Unmarshal([]byte(d.raw), bc)
				bc.Type = d.name
				switch bc.Type {
				case "slack":
					bc.Decode(&SlackSettings{})
				case "web":
					bc.Decode(&WebChatSettings{})
				case "web_client":
					bc.Decode(&WebChatClientSettings{})
				case "teams_webhook":
					bc.Decode(&TeamsWebhookSettings{})
				}
				chs[d.name] = bc
			}
			return chs
		}(),
	}

	t.Run("test for original", func(t *testing.T) {
		assert.Equal(t, 2, len(original.Tools.Web.Tavily.APIKeys))
		assert.Equal(t, "key1", original.Tools.Web.Tavily.APIKeys[0].String())
	})

	cfg2 := &Config{}
	t.Run("test for json", func(t *testing.T) {
		marshal, err := json.Marshal(original)
		require.NoError(t, err)
		t.Logf("json: %s", string(marshal))
		// Set secrets show the placeholder, channel settings leave them out,
		// and no value is shown.
		assert.Contains(t, string(marshal), `"api_keys":"[NOT_HERE]"`)
		for _, value := range []string{
			"brave_key", "key1", "slack_bot_token", "slack_app_token", "web_token", "web_client_token", "teams_webhook_url",
		} {
			assert.NotContains(t, string(marshal), value)
		}
		assert.NotContains(t, string(marshal), `"token"`)
		assert.NotContains(t, string(marshal), `"bot_token"`)
		assert.NotContains(t, string(marshal), `"webhook_url"`)
		assert.Contains(t, string(marshal), `"title":"Ops"`)

		err = json.Unmarshal(marshal, cfg2)
		require.NoError(t, err)
		assert.True(t, cfg2.Tools.Web.Tavily.Enabled)
		assert.Empty(t, cfg2.Tools.Web.Tavily.APIKeys)
		assert.Empty(t, cfg2.Tools.Web.Brave.APIKeys)
	})

	t.Run("test for save yaml", func(t *testing.T) {
		// Save
		err := saveSecurityConfig(secPath, original)
		require.NoError(t, err)

		// Verify file was created with correct permissions
		info, err := os.Stat(secPath)
		require.NoError(t, err)
		// Windows has no POSIX permission bits -- os.Chmod there can only
		// toggle the read-only flag, so a 0o600 file reports 0o666. Asserting
		// the exact mode tested the OS, not the code. What matters is that the
		// file is not world-readable where that is a real distinction.
		if runtime.GOOS != "windows" {
			assert.Equal(t, os.FileMode(0o600), info.Mode())
		}

		file, err := os.ReadFile(secPath)
		assert.NoError(t, err)
		t.Logf("%s", string(file))

		// Parse saved YAML and verify channelTestSaveConfig_EncryptsPlaintextAPIKey secure fields are present
		var saved struct {
			ChannelList map[string]map[string]any `yaml:"channel_list"`
		}
		require.NoError(t, yaml.Unmarshal(file, &saved))
		channels := saved.ChannelList
		getSetting := func(name string) map[string]any {
			return channels[name]["settings"].(map[string]any)
		}
		assert.Contains(t, getSetting("slack")["bot_token"], "slack_bot_token")
		assert.Contains(t, getSetting("slack")["app_token"], "slack_app_token")
		assert.Contains(t, getSetting("web")["token"], "web_token")
		assert.NotContains(t, getSetting("web"), "ping_interval")
		assert.Contains(t, getSetting("web_client")["token"], "web_client_token")
		teamsOps := getSetting("teams_webhook")["webhooks"].(map[string]any)["ops"].(map[string]any)
		assert.Equal(t, "teams_webhook_url", teamsOps["webhook_url"])
		assert.NotContains(t, teamsOps, "title")

		// Rewrite file with deterministic content for load test (use channel_list)
		yamlOutput := `channel_list:
  slack:
    settings:
      bot_token: slack_bot_token
      app_token: slack_app_token
  web:
    settings:
      token: web_token
  teams_webhook:
    settings:
      webhooks:
        ops:
          webhook_url: teams_webhook_url
  web_client:
    settings:
      token: web_client_token
web:
  brave:
    api_keys:
      - brave_key
  tavily:
    api_keys:
      - key1
      - key2
skills:
  registries:
    github:
      auth_token: github_token
`
		err = os.WriteFile(secPath, []byte(yamlOutput), 0o600)
		require.NoError(t, err)
	})

	t.Run("test for load yaml", func(t *testing.T) {
		// Load
		cfg := cfg2
		err := loadSecurityConfig(cfg, secPath)
		require.NoError(t, err)

		t.Logf("%+v", cfg)
		t.Logf("%+v", cfg.Tools.Web.Brave.APIKeys)
		require.EqualValues(t, 2, len(cfg.Tools.Web.Tavily.APIKeys))
		assert.Equal(t, "key1", cfg.Tools.Web.Tavily.APIKeys[0].String())
		assert.Equal(t, "key2", cfg.Tools.Web.Tavily.APIKeys[1].String())
		assert.EqualValues(t, original.Tools.Web.Brave.APIKeys, cfg.Tools.Web.Brave.APIKeys)
		github, ok := cfg.Tools.Skills.Registries.Get("github")
		require.True(t, ok)
		assert.Equal(t, "github_token", github.AuthToken.String())
		slack, err := cfg.Channels.Get("slack").GetDecoded()
		require.NoError(t, err)
		assert.Equal(t, "slack_bot_token", slack.(*SlackSettings).BotToken.String())
		assert.Equal(t, "slack_app_token", slack.(*SlackSettings).AppToken.String())
		web, err := cfg.Channels.Get("web").GetDecoded()
		require.NoError(t, err)
		assert.Equal(t, "web_token", web.(*WebChatSettings).Token.String())
		assert.Equal(t, 30, web.(*WebChatSettings).PingInterval)
		teams, err := cfg.Channels.Get("teams_webhook").GetDecoded()
		require.NoError(t, err)
		teamsOps := teams.(*TeamsWebhookSettings).Webhooks["ops"]
		assert.Equal(t, "teams_webhook_url", teamsOps.WebhookURL.String())
		assert.Equal(t, "Ops", teamsOps.Title)
	})

	t.Run("test for env overwrite", func(t *testing.T) {
		// This will throw a COMPILER ERROR if SecureString doesn't
		// correctly implement the yaml.Marshaler interface.
		var _ yaml.Marshaler = (*SecureString)(nil)
		// If you are using Value types in your config, also check:
		var _ yaml.Marshaler = SecureString{}

		// Set up a fresh config with a slack channel
		envCfg := &Config{
			Channels: ChannelsConfig{
				"slack": {
					Enabled:  true,
					Type:     "slack",
					Settings: RawNode(`{"enabled":true,"app_token":"slack_app_token"}`),
				},
			},
			Tools: original.Tools,
		}

		t.Setenv("COMPA_CHANNELS_SLACK_APP_TOKEN", "slack_app_token_env")
		t.Setenv("COMPA_TOOLS_WEB_BRAVE_API_KEYS", "brave_key_env,abc")

		require.NoError(t, env.Parse(envCfg))
		// Channel env overrides need explicit handling since ChannelsConfig is map-based
		require.NoError(t, InitChannelList(envCfg.Channels))

		bc := envCfg.Channels.Get("slack")
		decoded, err := bc.GetDecoded()
		require.NoError(t, err)
		slackCfg := decoded.(*SlackSettings)
		assert.Equal(t, "slack_app_token_env", slackCfg.AppToken.raw)
		assert.Equal(t, "brave_key_env", envCfg.Tools.Web.Brave.APIKeys[0].raw)
		assert.Equal(t, "abc", envCfg.Tools.Web.Brave.APIKeys[1].raw)
	})
}
