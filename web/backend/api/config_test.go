package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/logger"
)

func TestHandlePatchConfig_PreservesTurnProfile(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.Agents.Defaults.TurnProfile = config.TurnProfileConfig{
		Enabled:      true,
		History:      config.TurnProfileBlock{Mode: config.TurnProfileModeOff},
		SystemPrompt: config.TurnProfileBlock{Mode: config.TurnProfileModeOff},
		Skills:       config.TurnProfileBlock{Mode: config.TurnProfileModeOff},
		Tools: config.TurnProfileBlock{
			Mode:  config.TurnProfileModeCustom,
			Allow: []string{"web_search", "web_fetch"},
		},
	}
	if saveErr := config.SaveConfig(configPath, cfg); saveErr != nil {
		t.Fatalf("SaveConfig() error = %v", saveErr)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"agents": {
			"defaults": {
				"max_tokens": 1234
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	updated, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(updated) error = %v", err)
	}
	profile := updated.Agents.Defaults.TurnProfile
	if profile.Tools.Mode != config.TurnProfileModeCustom ||
		strings.Join(profile.Tools.Allow, ",") != "web_search,web_fetch" {
		t.Fatalf("profile tools = %#v, want custom web_search/web_fetch", profile.Tools)
	}
}

func TestHandlePatchConfig_RejectsInvalidTurnProfile(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"agents": {
			"defaults": {
				"turn_profile": {
					"enabled": true,
					"history": { "mode": "custom" }
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d, body=%s",
			rec.Code,
			http.StatusBadRequest,
			rec.Body.String(),
		)
	}
	if !strings.Contains(rec.Body.String(), "history.mode custom is not supported") {
		t.Fatalf("body=%s, want turn profile validation error", rec.Body.String())
	}

	if _, err := config.LoadConfig(configPath); err != nil {
		t.Fatalf("LoadConfig() after rejected patch error = %v", err)
	}
}

func assertGatewayLogLevelApplied(t *testing.T, method, body string, want logger.LogLevel) {
	t.Helper()

	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	initialLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() {
		logger.SetLevel(initialLevel)
	})

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(method, "/api/config", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"%s /api/config status = %d, want %d, body=%s",
			method,
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}
	if got := logger.GetLevel(); got != want {
		t.Fatalf("logger.GetLevel() = %v, want %v", got, want)
	}
}

func TestHandleUpdateConfig_PreservesExecAllowRemoteDefaultWhenOmitted(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(`{
		"agents": {
			"defaults": {
				"workspace": "~/.compa/workspace"
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !cfg.Tools.Exec.AllowRemote {
		t.Fatal("tools.exec.allow_remote should remain true when omitted from PUT /api/config")
	}
}

func TestHandleGetConfigRedactsProviderInstanceSecrets(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	cfg.ProviderInstances = []*config.ProviderInstanceConfig{{
		ID: "secret-provider", ProviderKind: "openai", Adapter: config.ProviderAdapterOpenAICompatible, Protocol: "openai",
		Endpoint:          "https://secret.example.test/v1",
		AuthConnectionRef: "credential:secret-provider",
		Headers:           map[string]string{"X-Api-Key": "sk-raw-header-must-not-leak"},
		Settings:          map[string]any{"tenant": "setting-must-not-leak"},
		Runtime:           &config.ProviderInstanceRuntime{RequestTimeout: 42},
		State:             config.ProviderInstanceStateEnabled,
	}}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, secret := range []string{"sk-raw-header-must-not-leak", "setting-must-not-leak"} {
		if strings.Contains(body, secret) {
			t.Fatalf("GET /api/config exposed %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"request_timeout":42`) {
		t.Fatalf("GET /api/config should carry the instance's runtime settings: %s", body)
	}
}

func TestHandlePatchConfig_RejectsInvalidExecRegexPatterns(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"tools": {
			"exec": {
				"custom_deny_patterns": ["("]
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d, body=%s",
			rec.Code,
			http.StatusBadRequest,
			rec.Body.String(),
		)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("custom_deny_patterns")) {
		t.Fatalf(
			"expected validation error mentioning custom_deny_patterns, body=%s",
			rec.Body.String(),
		)
	}
}

func TestHandlePatchConfig_AllowsInvalidExecRegexPatternsWhenExecDisabled(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"tools": {
			"exec": {
				"enabled": false,
				"custom_deny_patterns": ["("],
				"custom_allow_patterns": ["("]
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestHandlePatchConfig_SavesChannelListSettingsPatch(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"slack": {
				"enabled": true,
				"allow_from": ["U_PATCH_USER"],
				"settings": {
					"bot_token": "xoxb-patch-token",
					"app_token": "xapp-patch-token"
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	bc := cfg.Channels[config.ChannelSlack]
	if !bc.Enabled {
		t.Fatal("slack should be enabled after PATCH")
	}
	if len(bc.AllowFrom) != 1 || bc.AllowFrom[0] != "U_PATCH_USER" {
		t.Fatalf("slack allow_from = %#v, want [\"U_PATCH_USER\"]", bc.AllowFrom)
	}
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	if got := slackCfg.BotToken.String(); got != "xoxb-patch-token" {
		t.Fatalf("slack bot_token = %q, want %q", got, "xoxb-patch-token")
	}
	if got := slackCfg.AppToken.String(); got != "xapp-patch-token" {
		t.Fatalf("slack app_token = %q, want %q", got, "xapp-patch-token")
	}
}

func TestHandlePatchConfig_NormalizesStringChannelArrayFields(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"web": {
				"type": "web",
				"allow_from": " ou_a\u200b，\u2060ou_b\tou_c\u202e，ou_a ",
				"group_trigger": {
					"prefixes": "/，!;\n?，/"
				},
				"settings": {
					"allow_origins": "https://a.example.com，http://localhost:5173，https://a.example.com"
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	webChatChannel := cfg.Channels[config.ChannelWeb]
	if len(webChatChannel.AllowFrom) != 3 ||
		webChatChannel.AllowFrom[0] != "ou_a" ||
		webChatChannel.AllowFrom[1] != "ou_b" ||
		webChatChannel.AllowFrom[2] != "ou_c" {
		t.Fatalf(
			"web allow_from = %#v, want [\"ou_a\", \"ou_b\", \"ou_c\"]",
			webChatChannel.AllowFrom,
		)
	}
	// group_trigger is no longer a setting: an older client's string
	// prefixes still save, and are dropped.
	if len(webChatChannel.GroupTrigger.Prefixes) != 0 {
		t.Fatalf("web group_trigger.prefixes = %#v, want them dropped", webChatChannel.GroupTrigger.Prefixes)
	}

	decoded, err := webChatChannel.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() web error = %v", err)
	}
	webChatCfg := decoded.(*config.WebChatSettings)
	if len(webChatCfg.AllowOrigins) != 2 ||
		webChatCfg.AllowOrigins[0] != "https://a.example.com" ||
		webChatCfg.AllowOrigins[1] != "http://localhost:5173" {
		t.Fatalf(
			"web allow_origins = %#v, want [\"https://a.example.com\", \"http://localhost:5173\"]",
			webChatCfg.AllowOrigins,
		)
	}
}

func TestHandlePatchConfig_NormalizesSingleNumericAllowFrom(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"slack": {
				"type": "slack",
				"allow_from": 123456
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	slackChannel := cfg.Channels[config.ChannelSlack]
	if len(slackChannel.AllowFrom) != 1 || slackChannel.AllowFrom[0] != "123456" {
		t.Fatalf("slack allow_from = %#v, want [\"123456\"]", slackChannel.AllowFrom)
	}
}

func TestHandlePatchConfig_RejectsInvalidChannelArrayFields(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	slackChannel := cfg.Channels[config.ChannelSlack]
	slackChannel.AllowFrom = config.FlexibleStringSlice{"existing-user"}
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	tests := []struct {
		name string
		body string
	}{
		{
			name: "object allow_from",
			body: `{
				"channel_list": {
					"slack": {
						"type": "slack",
						"allow_from": {"id": "bad"}
					}
				}
			}`,
		},
		{
			name: "boolean allow_from",
			body: `{
				"channel_list": {
					"slack": {
						"type": "slack",
						"allow_from": true
					}
				}
			}`,
		},
		{
			name: "object settings array",
			body: `{
				"channel_list": {
					"web": {
						"type": "web",
						"settings": {
							"allow_origins": {"origin": "https://a.example.com"}
						}
					}
				}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(configPath)
			mux := http.NewServeMux()
			h.RegisterRoutes(mux)

			req := httptest.NewRequest(
				http.MethodPatch,
				"/api/config",
				bytes.NewBufferString(tt.body),
			)
			req.Header.Set("Content-Type", "application/json")

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf(
					"PATCH /api/config status = %d, want %d, body=%s",
					rec.Code,
					http.StatusBadRequest,
					rec.Body.String(),
				)
			}

			cfg, err := config.LoadConfig(configPath)
			if err != nil {
				t.Fatalf("LoadConfig() error = %v", err)
			}
			slackChannel := cfg.Channels[config.ChannelSlack]
			if len(slackChannel.AllowFrom) != 1 ||
				slackChannel.AllowFrom[0] != "existing-user" {
				t.Fatalf(
					"slack allow_from = %#v, want unchanged [\"existing-user\"]",
					slackChannel.AllowFrom,
				)
			}
		})
	}
}

func TestHandlePatchConfig_RejectsNegativeStreamingDeliveryValues(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"web": {
				"settings": {
					"streaming": {
						"enabled": true,
						"throttle_seconds": -1
					}
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusBadRequest,
			rec.Body.String(),
		)
	}
	if !strings.Contains(rec.Body.String(), "streaming.throttle_seconds") {
		t.Fatalf(
			"response body = %q, want streaming.throttle_seconds validation error",
			rec.Body.String(),
		)
	}
}

func TestHandlePatchConfig_ClearingAllowFromDoesNotLeaveEmptyStringItem(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	slackChannel := cfg.Channels[config.ChannelSlack]
	slackChannel.Enabled = true
	slackChannel.AllowFrom = config.FlexibleStringSlice{"U_EXISTING_USER"}
	decoded, err := slackChannel.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	slackCfg.BotToken = *config.NewSecureString("xoxb-existing")
	slackCfg.AppToken = *config.NewSecureString("xapp-existing")
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"slack": {
				"enabled": true,
				"allow_from": ""
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}

	cfg, err = config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	slackChannel = cfg.Channels[config.ChannelSlack]
	if len(slackChannel.AllowFrom) != 0 {
		t.Fatalf("slack allow_from = %#v, want empty slice", slackChannel.AllowFrom)
	}

	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(configPath) error = %v", err)
	}
	if strings.Contains(string(configData), `"allow_from": [""]`) {
		t.Fatalf(
			"config file should not contain empty-string allow_from item: %s",
			string(configData),
		)
	}
}

func TestHandlePatchConfig_CreatesMissingChannelWithTypeAndSecret(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	delete(cfg.Channels, config.ChannelSlack)
	if err = config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"channel_list": {
			"slack": {
				"enabled": true,
				"type": "slack",
				"settings": {
					"bot_token": "xoxb-patch-secret",
					"app_token": "xapp-patch-secret"
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}

	cfg, err = config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	bc := cfg.Channels[config.ChannelSlack]
	if bc == nil {
		t.Fatal("slack channel should exist after PATCH")
	}
	if got := bc.Type; got != config.ChannelSlack {
		t.Fatalf("slack type = %q, want %q", got, config.ChannelSlack)
	}
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	if got := slackCfg.BotToken.String(); got != "xoxb-patch-secret" {
		t.Fatalf("slack bot_token = %q, want %q", got, "xoxb-patch-secret")
	}
	if got := slackCfg.AppToken.String(); got != "xapp-patch-secret" {
		t.Fatalf("slack app_token = %q, want %q", got, "xapp-patch-secret")
	}
	configData, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(configPath) error = %v", err)
	}
	if bytes.Contains(configData, []byte("xoxb-patch-secret")) ||
		bytes.Contains(configData, []byte("xapp-patch-secret")) {
		t.Fatalf("config file leaked slack token: %s", string(configData))
	}
}

// setupWebChatEnabledEnv creates a test environment with web chat channel enabled and
// its token stored only in .security.yml (not in the JSON payload).
func setupWebChatEnabledEnv(t *testing.T) (string, func()) {
	t.Helper()

	tmp := t.TempDir()
	oldHome := os.Getenv("HOME")
	oldCompaHome := os.Getenv("COMPA_HOME")

	if err := os.Setenv("HOME", tmp); err != nil {
		t.Fatalf("set HOME: %v", err)
	}
	if err := os.Setenv("COMPA_HOME", filepath.Join(tmp, ".compa")); err != nil {
		t.Fatalf("set COMPA_HOME: %v", err)
	}

	cfg := config.DefaultConfig()
	bc := cfg.Channels["web"]
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	webChatCfg := decoded.(*config.WebChatSettings)
	bc.Enabled = true
	webChatCfg.Token = *config.NewSecureString("test-web-token")

	configPath := filepath.Join(tmp, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SaveConfig error: %v", err)
	}

	cleanup := func() {
		_ = os.Setenv("HOME", oldHome)
		if oldCompaHome == "" {
			_ = os.Unsetenv("COMPA_HOME")
		} else {
			_ = os.Setenv("COMPA_HOME", oldCompaHome)
		}
	}
	return configPath, cleanup
}

func TestHandleUpdateConfig_SucceedsWhenWebChatTokenInSecurityOnly(t *testing.T) {
	configPath, cleanup := setupWebChatEnabledEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// PUT request with web enabled but no token in JSON — token is in .security.yml
	req := httptest.NewRequest(http.MethodPut, "/api/config", bytes.NewBufferString(`{
		"agents": {
			"defaults": {
				"workspace": "~/.compa/workspace"
			}
		},
		"channel_list": {
			"web": {
				"enabled": true,
				"type": "web",
				"settings": {
					"ping_interval": 30,
					"read_timeout": 60,
					"write_timeout": 10,
					"max_connections": 100
				}
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PUT /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}
}

func TestHandlePatchConfig_SucceedsWhenWebChatTokenInSecurityOnly(t *testing.T) {
	configPath, cleanup := setupWebChatEnabledEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	// PATCH request changing an unrelated field — web token still in .security.yml
	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"gateway": {
			"log_level": "info"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}
}

func TestHandleUpdateConfig_AppliesGatewayLogLevel(t *testing.T) {
	assertGatewayLogLevelApplied(t, http.MethodPut, `{
		"agents": {
			"defaults": {
				"workspace": "~/.compa/workspace"
			}
		},
		"gateway": {
			"log_level": "error"
		}
	}`, logger.ERROR)
}

func TestHandlePatchConfig_AppliesGatewayLogLevel(t *testing.T) {
	assertGatewayLogLevelApplied(t, http.MethodPatch, `{
		"gateway": {
			"log_level": "debug"
		}
	}`, logger.DEBUG)
}

func TestHandlePatchConfig_PreservesDebugFlagOverride(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	initialLevel := logger.GetLevel()
	logger.SetLevel(logger.INFO)
	t.Cleanup(func() {
		logger.SetLevel(initialLevel)
	})

	h := NewHandler(configPath)
	h.SetDebug(true)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"gateway": {
			"log_level": "error"
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf(
			"PATCH /api/config status = %d, want %d, body=%s",
			rec.Code,
			http.StatusOK,
			rec.Body.String(),
		)
	}
	if got := logger.GetLevel(); got != logger.DEBUG {
		t.Fatalf("logger.GetLevel() = %v, want %v", got, logger.DEBUG)
	}
}

// A registry token sent in a PATCH is kept in .security.yml, and a later
// PATCH of another setting, whose merged config holds the placeholder for
// it, keeps the token.
func TestHandlePatchConfig_KeepsTheRegistryAuthToken(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	patch := func(body string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("PATCH /api/config status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
		}
	}

	patch(`{"tools":{"skills":{"registries":{"github":{"auth_token":"ghp-test-token"}}}}}`)
	patch(`{"agents":{"defaults":{"max_tool_iterations":7}}}`)

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	githubRegistry, ok := cfg.Tools.Skills.Registries.Get("github")
	if !ok {
		t.Fatal("github registry missing after PATCH")
	}
	if got := githubRegistry.AuthToken.String(); got != "ghp-test-token" {
		t.Fatalf("github registry auth token = %q, want %q", got, "ghp-test-token")
	}
	if got := githubRegistry.BaseURL; got != "https://github.com" {
		t.Fatalf("github registry base_url = %q, want %q", got, "https://github.com")
	}
	if got := cfg.Agents.Defaults.MaxToolIterations; got != 7 {
		t.Fatalf("max_tool_iterations = %d, want 7", got)
	}

	rawConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile(configPath) error = %v", err)
	}
	if strings.Contains(string(rawConfig), "ghp-test-token") {
		t.Fatalf("config.json holds the token:\n%s", rawConfig)
	}
}

func TestHandlePatchConfig_AllowsInvalidDenyRegexPatternsWhenDenyPatternsDisabled(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPatch, "/api/config", bytes.NewBufferString(`{
		"tools": {
			"exec": {
				"enabled": true,
				"enable_deny_patterns": false,
				"custom_deny_patterns": ["("]
			}
		}
	}`))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

// testCommandPatterns is a helper that sets up a handler and sends a test-command-patterns request.
func testCommandPatterns(t *testing.T, configPath string, body string) *httptest.ResponseRecorder {
	t.Helper()
	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/config/test-command-patterns",
		bytes.NewBufferString(body),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestHandleTestCommandPatterns_MatchesWhitelist(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["^echo\\s+hello"],
		"deny_patterns": ["^rm\\s+-rf"],
		"command": "echo hello world"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"allowed":true`)) {
		t.Fatalf("expected allowed=true, body=%s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"blocked":true`)) {
		t.Fatalf("expected blocked=false when whitelist matches, body=%s", rec.Body.String())
	}
}

func TestHandleTestCommandPatterns_MatchesBlacklistNotWhitelist(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["^echo\\s+hello"],
		"deny_patterns": ["^rm\\s+-rf"],
		"command": "rm -rf /tmp"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"blocked":true`)) {
		t.Fatalf("expected blocked=true, body=%s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"allowed":true`)) {
		t.Fatalf(
			"expected allowed=false when blacklist matches but not whitelist, body=%s",
			rec.Body.String(),
		)
	}
}

// The blacklist outranks the whitelist, as in the exec tool, where a
// whitelist match skips only the built-in deny patterns.
func TestHandleTestCommandPatterns_BlacklistOutranksWhitelist(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["^git\\s+push\\b"],
		"deny_patterns": ["\\s--force\\b"],
		"command": "git push origin main --force"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	var body struct {
		Allowed          bool    `json:"allowed"`
		Blocked          bool    `json:"blocked"`
		MatchedBlacklist *string `json:"matched_blacklist"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if body.Allowed || !body.Blocked || body.MatchedBlacklist == nil || *body.MatchedBlacklist != `\s--force\b` {
		t.Fatalf("answer = %s, want blocked by the blacklist pattern", rec.Body.String())
	}
}

func TestHandleTestCommandPatterns_MatchesNeither(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["^echo\\s+hello"],
		"deny_patterns": ["^rm\\s+-rf"],
		"command": "ls -la"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"allowed":true`)) {
		t.Fatalf("expected allowed=false, body=%s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"blocked":true`)) {
		t.Fatalf("expected blocked=false, body=%s", rec.Body.String())
	}
}

func TestHandleTestCommandPatterns_CaseInsensitiveWithGoFlag(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["(?i)^ECHO"],
		"deny_patterns": [],
		"command": "echo hello"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"allowed":true`)) {
		t.Fatalf("expected allowed=true with Go (?i) flag, body=%s", rec.Body.String())
	}
}

func TestHandleTestCommandPatterns_EmptyPatterns(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": [],
		"deny_patterns": [],
		"command": "rm -rf /tmp"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"allowed":true`)) {
		t.Fatalf("expected allowed=false with empty patterns, body=%s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte(`"blocked":true`)) {
		t.Fatalf("expected blocked=false with empty patterns, body=%s", rec.Body.String())
	}
}

// A pattern that does not compile is named, not skipped: the exec tool cannot
// apply it either, so "no match" would mislead.
func TestHandleTestCommandPatterns_InvalidRegexIsNamed(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": ["([[", "^echo"],
		"deny_patterns": ["^rm(", "^ok"],
		"command": "echo hello"
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	var body struct {
		Error           string   `json:"error"`
		InvalidPatterns []string `json:"invalid_patterns"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v", rec.Body.String(), err)
	}
	if len(body.InvalidPatterns) != 2 ||
		!strings.Contains(body.InvalidPatterns[0], `"([["`) || !strings.Contains(body.InvalidPatterns[1], `"^rm("`) ||
		!strings.Contains(body.Error, "([[") {
		t.Fatalf("answer = %+v, want both invalid patterns named", body)
	}
}

func TestHandleTestCommandPatterns_ReturnsMatchedPattern(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	rec := testCommandPatterns(t, configPath, `{
		"allow_patterns": [],
		"deny_patterns": ["\\$(?i)[a-zA-Z_]*(SECRET|KEY|PASSWORD|TOKEN|AUTH)[a-zA-Z0-9_]*"],
		"command": "echo $GITHUB_API_KEY"
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`"blocked":true`)) {
		t.Fatalf("expected blocked=true, body=%s", rec.Body.String())
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte(`matched_blacklist`)) {
		t.Fatalf("expected matched_blacklist field, body=%s", rec.Body.String())
	}
}

func TestHandleTestCommandPatterns_InvalidJSON(t *testing.T) {
	configPath, cleanup := setupCredentialTestEnv(t)
	defer cleanup()

	h := NewHandler(configPath)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	req := httptest.NewRequest(
		http.MethodPost,
		"/api/config/test-command-patterns",
		bytes.NewBufferString(`{invalid json}`),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf(
			"status = %d, want %d, body=%s",
			rec.Code,
			http.StatusBadRequest,
			rec.Body.String(),
		)
	}
}

func TestApplyConfigSecretsFromMap_SlackToken(t *testing.T) {
	cfg := config.DefaultConfig()
	bc := cfg.Channels["slack"]
	bc.Enabled = true
	// Pre-decode so extend is populated
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	slackCfg.BotToken = *config.NewSecureString("original-token")

	raw := map[string]any{
		"channel_list": map[string]any{
			"slack": map[string]any{
				"enabled":   true,
				"bot_token": "secret-from-api",
			},
		},
	}

	applyConfigSecretsFromMap(cfg, raw)

	if got := slackCfg.BotToken.String(); got != "secret-from-api" {
		t.Fatalf("slack bot_token = %q, want %q", got, "secret-from-api")
	}
}

func TestApplyConfigSecretsFromMap_TeamsWebhook(t *testing.T) {
	// applyConfigSecretsFromMap recurses into nested maps to find
	// SecureString fields at any depth (e.g. webhook_url inside webhooks map).
	cfg := config.DefaultConfig()
	bc := &config.Channel{Enabled: true, Type: config.ChannelTeamsWebHook}
	cfg.Channels["teams_webhook"] = bc
	target := &config.TeamsWebhookSettings{
		Webhooks: map[string]config.TeamsWebhookTarget{
			"default": {
				WebhookURL: *config.NewSecureString("https://example.com/hook1"),
				Title:      "Default",
			},
		},
	}
	if err := bc.Decode(target); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}

	raw := map[string]any{
		"channel_list": map[string]any{
			"teams_webhook": map[string]any{
				"enabled": true,
				"settings": map[string]any{
					"webhooks": map[string]any{
						"default": map[string]any{
							"webhook_url": "https://example.com/hook-updated",
							"title":       "Default Updated",
						},
					},
				},
			},
		},
	}

	applyConfigSecretsFromMap(cfg, raw)

	// Verify the decoded struct has the updated SecureString value
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	twCfg, ok := decoded.(*config.TeamsWebhookSettings)
	if !ok {
		t.Fatalf("expected *TeamsWebhookSettings, got %T", decoded)
	}

	hookURL := twCfg.Webhooks["default"].WebhookURL
	if got := hookURL.String(); got != "https://example.com/hook-updated" {
		t.Fatalf("webhook_url = %q, want %q", got, "https://example.com/hook-updated")
	}
	// Note: title is a plain string, not a SecureString, so it is NOT updated
	// by applyConfigSecretsFromMap (only secure fields are handled).
}

func TestApplyConfigSecretsFromMap_MultipleChannels(t *testing.T) {
	cfg := config.DefaultConfig()

	// Setup slack
	bc := cfg.Channels["slack"]
	bc.Enabled = true
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() slack error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	slackCfg.BotToken = *config.NewSecureString("old-slack-token")

	// Setup web
	bc = cfg.Channels["web"]
	bc.Enabled = true
	decoded, err = bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() web error = %v", err)
	}
	webCfg := decoded.(*config.WebChatSettings)
	webCfg.Token = *config.NewSecureString("old-web-token")

	raw := map[string]any{
		"channel_list": map[string]any{
			"slack": map[string]any{
				"enabled": true,
				"settings": map[string]any{
					"bot_token": "new-slack-token",
				},
			},
			"web": map[string]any{
				"enabled": true,
				"settings": map[string]any{
					"token": "new-web-token",
				},
			},
		},
	}

	applyConfigSecretsFromMap(cfg, raw)

	if got := slackCfg.BotToken.String(); got != "new-slack-token" {
		t.Fatalf("slack bot_token = %q, want %q", got, "new-slack-token")
	}
	if got := webCfg.Token.String(); got != "new-web-token" {
		t.Fatalf("web token = %q, want %q", got, "new-web-token")
	}
}

func TestApplyConfigSecretsFromMap_SkipsNonStringValues(t *testing.T) {
	cfg := config.DefaultConfig()
	bc := cfg.Channels["slack"]
	bc.Enabled = true
	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	slackCfg.BotToken = *config.NewSecureString("original-token")

	raw := map[string]any{
		"channel_list": map[string]any{
			"slack": map[string]any{
				"enabled":   true,
				"bot_token": 12345, // not a string, should be skipped
			},
		},
	}

	applyConfigSecretsFromMap(cfg, raw)

	if got := slackCfg.BotToken.String(); got != "original-token" {
		t.Fatalf("slack bot_token = %q, want %q", got, "original-token")
	}
}

func TestApplyConfigSecretsFromMap_ChannelNotDecodedYet(t *testing.T) {
	cfg := config.DefaultConfig()
	bc := cfg.Channels["slack"]
	bc.Enabled = true
	// Don't decode — let the function handle lazy decoding
	bc.Type = config.ChannelSlack

	raw := map[string]any{
		"channel_list": map[string]any{
			"slack": map[string]any{
				"enabled":   true,
				"bot_token": "lazy-decoded-token",
			},
		},
	}

	applyConfigSecretsFromMap(cfg, raw)

	decoded, err := bc.GetDecoded()
	if err != nil {
		t.Fatalf("GetDecoded() error = %v", err)
	}
	slackCfg := decoded.(*config.SlackSettings)
	if got := slackCfg.BotToken.String(); got != "lazy-decoded-token" {
		t.Fatalf("slack bot_token = %q, want %q", got, "lazy-decoded-token")
	}
}
