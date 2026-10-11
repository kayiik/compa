// Compa - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Compa contributors

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test JSON unmarshal of private fields (unexported fields are never filled, with or without json tag).
func TestJSONUnmarshalPrivateFields(t *testing.T) {
	type testStruct struct {
		PublicField  string `json:"public"`
		privateField string
	}

	data := `{"public": "pub", "privateField": "priv"}`
	var s testStruct
	if err := json.Unmarshal([]byte(data), &s); err != nil {
		t.Fatalf("JSON unmarshal failed: %v", err)
	}

	t.Logf("PublicField: %s", s.PublicField)
	t.Logf("privateField: %s", s.privateField)

	if s.PublicField != "pub" {
		t.Errorf("PublicField = %q, want 'pub'", s.PublicField)
	}
	if s.privateField != "" {
		t.Errorf("privateField = %q, want empty because unexported fields are ignored", s.privateField)
	}
}

func TestSecurityConfigIntegration(t *testing.T) {
	t.Run("Full workflow with security references", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create config.json with direct security values using the current schema.
		configPath := filepath.Join(tmpDir, "config.json")
		configContent := `{
  "channel_list": {
    "slack": {
      "enabled": true,
      "settings": {
        "bot_token": "token-from-config-json-direct"
      }
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": true,
        "api_keys": ["BSA-from-config-json-direct"]
      },
      "tavily": {
        "enabled": true,
        "api_keys": ["tvly-from-config-json-direct"]
      }
    },
    "skills": {
      "registries": {
        "github": {
          "enabled": true,
          "auth_token": "ghp-from-config-json-direct"
        }
      }
    }
  }
}`
		err := os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		// Create .security.yml with different values; they override config.json.
		securityPath := filepath.Join(tmpDir, SecurityConfigFile)
		securityContent := `web:
  tavily:
    api_keys:
      - "tvly-from-security-yml"

channel_list:
  slack:
    settings:
      bot_token: "token-from-security-yml"

skills:
  registries:
    github:
      auth_token: "ghp-from-security-yml"`
		err = os.WriteFile(securityPath, []byte(securityContent), 0o600)
		require.NoError(t, err)

		// Load config and verify .security.yml values take precedence
		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Verify web tool API key from .security.yml takes precedence
		assert.Equal(t, "tvly-from-security-yml", cfg.Tools.Web.Tavily.APIKey())
		require.Len(t, cfg.Tools.Web.Tavily.APIKeys, 1)

		// Verify channel token from .security.yml takes precedence
		var slackTokenCfg *SlackSettings
		if bc := cfg.Channels.Get("slack"); bc != nil {
			if decoded, err := bc.GetDecoded(); err == nil && decoded != nil {
				slackTokenCfg = decoded.(*SlackSettings)
			}
		}
		require.NotNil(t, slackTokenCfg)
		assert.Equal(t, "token-from-security-yml", slackTokenCfg.BotToken.String())

		// Verify web tool API key from config.json is kept when .security.yml has none
		assert.Equal(t, "BSA-from-config-json-direct", cfg.Tools.Web.Brave.APIKey())

		// Verify skills registry token is resolved
		github, ok := cfg.Tools.Skills.Registries.Get("github")
		require.True(t, ok)
		assert.Equal(t, "ghp-from-security-yml", github.AuthToken.String())
	})
}

func TestSecurityConfigWithAPIKeysArray(t *testing.T) {
	t.Run("Multiple API keys via security", func(t *testing.T) {
		tmpDir := t.TempDir()

		configPath := filepath.Join(tmpDir, "config.json")
		configContent := `{
  "tools": {
    "web": {
      "tavily": {
        "enabled": true
      }
    }
  }
}`
		err := os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		// Create .security.yml
		securityPath := filepath.Join(tmpDir, SecurityConfigFile)
		securityContent := `web:
  tavily:
    api_keys:
      - "tvly-key-1"
      - "tvly-key-2"
      - "tvly-key-3"
`
		err = os.WriteFile(securityPath, []byte(securityContent), 0o600)
		require.NoError(t, err)

		// Load config
		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)

		assert.Equal(t, []string{"tvly-key-1", "tvly-key-2", "tvly-key-3"}, cfg.Tools.Web.Tavily.APIKeys.Values())
		assert.Equal(t, "tvly-key-1", cfg.Tools.Web.Tavily.APIKey())
	})
}

func TestAllSecurityKeysAccessible(t *testing.T) {
	t.Run("All security keys accessible via Key() methods including file://", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create test files for file:// references
		braveAPIKeyFile := filepath.Join(tmpDir, "brave_api_key.txt")
		err := os.WriteFile(braveAPIKeyFile, []byte("BSA-brave-from-file-67890"), 0o600)
		require.NoError(t, err)

		tavilyAPIKeyFile := filepath.Join(tmpDir, "tavily_api_key.txt")
		err = os.WriteFile(tavilyAPIKeyFile, []byte("tvly-tavily-from-file-11111"), 0o600)
		require.NoError(t, err)

		perplexityAPIKeyFile := filepath.Join(tmpDir, "perplexity_api_key.txt")
		err = os.WriteFile(perplexityAPIKeyFile, []byte("pplx-perplexity-from-file-22222"), 0o600)
		require.NoError(t, err)

		kagiAPIKeyFile := filepath.Join(tmpDir, "kagi_api_key.txt")
		err = os.WriteFile(kagiAPIKeyFile, []byte("kagi-from-file-33333"), 0o600)
		require.NoError(t, err)

		githubTokenFile := filepath.Join(tmpDir, "github_token.txt")
		err = os.WriteFile(githubTokenFile, []byte("ghp-github-from-file-abc123"), 0o600)
		require.NoError(t, err)

		clawhubAuthTokenFile := filepath.Join(tmpDir, "clawhub_auth_token.txt")
		err = os.WriteFile(clawhubAuthTokenFile, []byte("clawhub-auth-token-from-file"), 0o600)
		require.NoError(t, err)

		webClientTokenFile := filepath.Join(tmpDir, "web_client_token.txt")
		err = os.WriteFile(webClientTokenFile, []byte("web-client-token-from-file"), 0o600)
		require.NoError(t, err)

		// Create config.json without sensitive values (they'll be in .security.yml)
		configPath := filepath.Join(tmpDir, "config.json")
		configContent := `{
  "channel_list": {
    "slack": {
      "enabled": true
    },
    "web": {
      "enabled": true
    },
    "web_client": {
      "enabled": true,
      "settings": {
        "url": "ws://localhost:18790/ws"
      }
    },
    "teams_webhook": {
      "enabled": true,
      "settings": {
        "webhooks": {
          "ops": {"title": "Ops"}
        }
      }
    },
    "slack_webhook": {
      "enabled": true,
      "settings": {
        "webhooks": {
          "alerts": {"username": "compa"}
        }
      }
    }
  },
  "tools": {
    "web": {
      "brave": {
        "enabled": true
      },
      "tavily": {
        "enabled": true
      },
      "perplexity": {
        "enabled": true
      },
      "kagi": {
        "enabled": true
      },
      "glm_search": {
        "enabled": true
      }
    },
    "skills": {
      "registries": {
        "github": {
          "enabled": true
        }
      }
    }
  }
}`
		err = os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		// Create .security.yml with file:// references and plaintext values
		securityPath := filepath.Join(tmpDir, SecurityConfigFile)
		securityContent := `channel_list:
  slack:
    settings:
      bot_token: "xoxb-slack-bot-token-123"
      app_token: "xapp-slack-app-token-456"
  web:
    settings:
      token: "web_test_token"
  web_client:
    settings:
      token: "file://web_client_token.txt"
  teams_webhook:
    settings:
      webhooks:
        ops:
          webhook_url: "https://teams.example/webhook/ops-secret"
  slack_webhook:
    settings:
      webhooks:
        alerts:
          webhook_url: "https://hooks.slack.example/services/T0/B0/alerts-secret"
web:
  brave:
    api_keys:
      - "file://brave_api_key.txt"
  tavily:
    api_keys:
      - "file://tavily_api_key.txt"
  perplexity:
    api_keys:
      - "file://perplexity_api_key.txt"
  kagi:
    api_keys:
      - "file://kagi_api_key.txt"
  glm_search:
    api_key: "glm-test-glm-search-key"

skills:
  registries:
    github:
      auth_token: "file://github_token.txt"
    clawhub:
      auth_token: "file://clawhub_auth_token.txt"
`
		err = os.WriteFile(securityPath, []byte(securityContent), 0o600)
		require.NoError(t, err)

		// Load config and verify all security keys are accessible
		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)
		require.NotNil(t, cfg)

		// Helper function to decode channel settings
		decodeChannel := func(name string) any {
			bc := cfg.Channels.Get(name)
			if bc == nil {
				return nil
			}
			decoded, _ := bc.GetDecoded()
			return decoded
		}

		// Helper to get SecureString value
		secureStr := func(s SecureString) string {
			return s.String()
		}

		// Verify Channel tokens via Key() methods
		// Slack
		slSec := decodeChannel("slack")
		assert.Equal(t, "xoxb-slack-bot-token-123", secureStr(slSec.(*SlackSettings).BotToken))
		assert.Equal(t, "xapp-slack-app-token-456", secureStr(slSec.(*SlackSettings).AppToken))
		t.Logf("Slack BotToken(): %s", secureStr(slSec.(*SlackSettings).BotToken))
		t.Logf("Slack AppToken(): %s", secureStr(slSec.(*SlackSettings).AppToken))

		// Web chat
		webSec := decodeChannel("web")
		assert.Equal(t, "web_test_token", secureStr(webSec.(*WebChatSettings).Token))
		t.Logf("WebChatSettings.Token(): %s", secureStr(webSec.(*WebChatSettings).Token))

		// Web chat client (token from a file:// reference)
		webClientSec := decodeChannel("web_client")
		assert.Equal(t, "ws://localhost:18790/ws", webClientSec.(*WebChatClientSettings).URL)
		assert.Equal(t, "web-client-token-from-file", secureStr(webClientSec.(*WebChatClientSettings).Token))
		t.Logf("WebChatClientSettings.Token(): %s", secureStr(webClientSec.(*WebChatClientSettings).Token))

		// Teams webhook
		teamsSec := decodeChannel("teams_webhook")
		teamsTarget := teamsSec.(*TeamsWebhookSettings).Webhooks["ops"]
		assert.Equal(t, "Ops", teamsTarget.Title)
		assert.Equal(t, "https://teams.example/webhook/ops-secret", secureStr(teamsTarget.WebhookURL))
		t.Logf("Teams WebhookURL(): %s", secureStr(teamsTarget.WebhookURL))

		// Slack webhook
		slackHookSec := decodeChannel("slack_webhook")
		slackHookTarget := slackHookSec.(*SlackWebhookSettings).Webhooks["alerts"]
		assert.Equal(t, "compa", slackHookTarget.Username)
		assert.Equal(
			t,
			"https://hooks.slack.example/services/T0/B0/alerts-secret",
			secureStr(slackHookTarget.WebhookURL),
		)
		t.Logf("Slack WebhookURL(): %s", secureStr(slackHookTarget.WebhookURL))

		// Verify Web tool API keys
		assert.Equal(t, "BSA-brave-from-file-67890", cfg.Tools.Web.Brave.APIKey())
		t.Logf("Brave APIKey(): %s", cfg.Tools.Web.Brave.APIKey())

		assert.Equal(t, "tvly-tavily-from-file-11111", cfg.Tools.Web.Tavily.APIKey())
		t.Logf("Tavily APIKey(): %s", cfg.Tools.Web.Tavily.APIKey())

		assert.Equal(t, "pplx-perplexity-from-file-22222", cfg.Tools.Web.Perplexity.APIKey())
		t.Logf("Perplexity APIKey(): %s", cfg.Tools.Web.Perplexity.APIKey())

		assert.Equal(t, "kagi-from-file-33333", cfg.Tools.Web.Kagi.APIKey())
		t.Logf("Kagi APIKey(): %s", cfg.Tools.Web.Kagi.APIKey())

		// GLM Search - Note: GLM uses SetAPIKey (lowercase) internally
		t.Logf("GLMSearch APIKey(): %s", cfg.Tools.Web.GLMSearch.APIKey.String())
		assert.Equal(t, "glm-test-glm-search-key", cfg.Tools.Web.GLMSearch.APIKey.String())

		// Verify Skills tokens
		github, ok := cfg.Tools.Skills.Registries.Get("github")
		assert.True(t, ok)
		assert.Equal(t, "ghp-github-from-file-abc123", github.AuthToken.String())
		t.Logf("GitHub AuthToken(): %s", github.AuthToken.String())

		clawHub, ok := cfg.Tools.Skills.Registries.Get("clawhub")
		assert.True(t, ok)
		assert.Equal(t, "clawhub-auth-token-from-file", clawHub.AuthToken.String())
		t.Logf("ClawHub AuthToken(): %s", clawHub.AuthToken.String())

		t.Log("All security keys are successfully accessible via their respective Key() methods")
	})

	t.Run("Github registry token supports security overlay", func(t *testing.T) {
		tmpDir := t.TempDir()

		githubTokenFile := filepath.Join(tmpDir, "github_registry_token.txt")
		err := os.WriteFile(githubTokenFile, []byte("ghp-github-registry-token-from-file"), 0o600)
		require.NoError(t, err)

		configPath := filepath.Join(tmpDir, "config.json")
		configContent := `{
  "tools": {
    "skills": {
      "registries": {
        "github": {
	          "enabled": true,
	          "proxy": "http://127.0.0.1:7890"
        }
      }
    }
  }
}`
		err = os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		securityPath := filepath.Join(tmpDir, SecurityConfigFile)
		securityContent := `skills:
  registries:
    github:
      auth_token: "file://github_registry_token.txt"
`
		err = os.WriteFile(securityPath, []byte(securityContent), 0o600)
		require.NoError(t, err)

		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)

		githubRegistry, ok := cfg.Tools.Skills.Registries.Get("github")
		require.True(t, ok)
		assert.Equal(t, "ghp-github-registry-token-from-file", githubRegistry.AuthToken.String())
		assert.Equal(t, "http://127.0.0.1:7890", githubRegistry.Param["proxy"])
	})

	t.Run("Custom registry token supports security overlay", func(t *testing.T) {
		tmpDir := t.TempDir()

		customTokenFile := filepath.Join(tmpDir, "custom_registry_token.txt")
		err := os.WriteFile(customTokenFile, []byte("custom-registry-token-from-file"), 0o600)
		require.NoError(t, err)

		configPath := filepath.Join(tmpDir, "config.json")
		configContent := `{
  "tools": {
    "skills": {
      "registries": {
        "custom": {
          "enabled": true,
          "base_url": "https://skills.example.com"
        }
      }
    }
  }
}`
		err = os.WriteFile(configPath, []byte(configContent), 0o644)
		require.NoError(t, err)

		securityPath := filepath.Join(tmpDir, SecurityConfigFile)
		securityContent := `skills:
  registries:
    custom:
      auth_token: "file://custom_registry_token.txt"
`
		err = os.WriteFile(securityPath, []byte(securityContent), 0o600)
		require.NoError(t, err)

		cfg, err := LoadConfig(configPath)
		require.NoError(t, err)

		customRegistry, ok := cfg.Tools.Skills.Registries.Get("custom")
		require.True(t, ok)
		assert.Equal(t, "https://skills.example.com", customRegistry.BaseURL)
		assert.Equal(t, "custom-registry-token-from-file", customRegistry.AuthToken.String())

		githubRegistry, ok := cfg.Tools.Skills.Registries.Get("github")
		require.True(t, ok)
		assert.Equal(t, "https://github.com", githubRegistry.BaseURL)
	})
}
