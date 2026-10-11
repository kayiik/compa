package channels

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/logger"
)

func TestToChannelHashes(t *testing.T) {
	logger.SetLevel(logger.DEBUG)
	cfg := config.DefaultConfig()
	results := toChannelHashes(cfg)
	assert.Equal(t, 0, len(results))
	logger.Debugf("results: %v", results)

	// Add whatsapp channel via map
	cfg2 := config.DefaultConfig()
	cfg2.Channels["whatsapp"] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelWhatsApp,
		Settings: config.RawNode(`{"enabled":true}`),
	}
	results2 := toChannelHashes(cfg2)
	assert.Equal(t, 1, len(results2))
	logger.Debugf("results2: %v", results2)
	added, removed := compareChannels(results, results2)
	assert.EqualValues(t, []string{"whatsapp"}, added)
	assert.EqualValues(t, []string(nil), removed)

	// Add slack channel
	cfg3 := config.DefaultConfig()
	cfg3.Channels["slack"] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelSlack,
		Settings: config.RawNode(`{"enabled":true,"bot_token":"test-token"}`),
	}
	results3 := toChannelHashes(cfg3)
	assert.Equal(t, 1, len(results3))
	logger.Debugf("results3: %v", results3)
	added, removed = compareChannels(results2, results3)
	assert.EqualValues(t, []string{"whatsapp"}, removed)
	assert.EqualValues(t, []string{"slack"}, added)

	// Modify slack channel — hash should change
	cfg3.Channels["slack"] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelSlack,
		Settings: config.RawNode(`{"enabled":true,"bot_token":"114314"}`),
	}
	results4 := toChannelHashes(cfg3)
	assert.Equal(t, 1, len(results4))
	logger.Debugf("results4: %v", results4)
	added, removed = compareChannels(results3, results4)
	assert.EqualValues(t, []string{"slack"}, removed)
	assert.EqualValues(t, []string{"slack"}, added)

	// toChannelConfig with slack
	cc, err := toChannelConfig(cfg3, added)
	assert.NoError(t, err)
	bc := cc.Get("slack")
	assert.NotNil(t, bc)
	var sc config.SlackSettings
	bc.Decode(&sc)
	assert.Equal(t, "114314", sc.BotToken.String())
	assert.Equal(t, true, bc.Enabled)

	// toChannelConfig with whatsapp (no enabled slack)
	cc, err = toChannelConfig(cfg2, added)
	assert.NoError(t, err)
	bc = cc.Get("slack")
	assert.Nil(t, bc)
}

func TestToChannelHashes_SerializationStability(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{
		Enabled:  true,
		Settings: config.RawNode(`{"enabled":true,"key":"value"}`),
	}
	h1 := toChannelHashes(cfg)

	// Same config should produce same hash
	cfg2 := config.DefaultConfig()
	cfg2.Channels["test"] = &config.Channel{
		Enabled:  true,
		Settings: config.RawNode(`{"enabled":true,"key":"value"}`),
	}
	h2 := toChannelHashes(cfg2)
	assert.Equal(t, h1["test"], h2["test"])
}

func TestCompareChannels_NoChanges(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["a"] = &config.Channel{Enabled: true, Settings: config.RawNode(`{}`)}
	cfg.Channels["b"] = &config.Channel{Enabled: true, Settings: config.RawNode(`{}`)}
	h := toChannelHashes(cfg)

	added, removed := compareChannels(h, h)
	assert.EqualValues(t, []string(nil), added)
	assert.EqualValues(t, []string(nil), removed)
}

func TestToChannelConfig_EmptyList(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{Enabled: true, Settings: config.RawNode(`{}`)}

	cc, err := toChannelConfig(cfg, []string{})
	assert.NoError(t, err)
	assert.Equal(t, 0, len(*cc))
}

func TestToChannelHashes_NonEnabledSkipped(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{Enabled: false, Settings: config.RawNode(`{"enabled":false}`)}

	h := toChannelHashes(cfg)
	assert.Equal(t, 0, len(h))
}

func TestToChannelHashes_InvalidJSON(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{
		Enabled:  true,
		Settings: config.RawNode(`invalid-json`),
	}

	// Should not panic, just skip the invalid entry
	h := toChannelHashes(cfg)
	assert.Equal(t, 0, len(h))
}

func TestToChannelHashes_RealWorldChannel(t *testing.T) {
	cfg := config.DefaultConfig()

	// Simulate a slack channel config
	slackSettings, _ := json.Marshal(map[string]any{
		"enabled":   true,
		"bot_token": "xoxb-123456",
		"app_token": "xapp-123456",
	})
	cfg.Channels["slack"] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelSlack,
		Settings: config.RawNode(slackSettings),
	}

	h := toChannelHashes(cfg)
	assert.Equal(t, 1, len(h))
	assert.Contains(t, h, "slack")
}

func TestToChannelHashes_MissingEnabledKey(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{
		Settings: config.RawNode(`{"key":"value"}`),
	}

	// Should not panic — the ok check safely handles the missing/false case
	assert.NotPanics(t, func() {
		_ = toChannelHashes(cfg)
	})
	h := toChannelHashes(cfg)
	assert.Equal(t, 0, len(h), "channel with Enabled=false (default) skipped")
}

func TestToChannelHashes_EnabledNotBool(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Channels["test"] = &config.Channel{
		Enabled:  false,
		Settings: config.RawNode(`{"enabled":"yes","boolField":true}`),
	}

	// Should not panic — string "enabled" won't match bool assertion, ok=false
	assert.NotPanics(t, func() {
		_ = toChannelHashes(cfg)
	})
	h := toChannelHashes(cfg)
	assert.Equal(t, 0, len(h), "string enabled not treated as true")
}

func TestToChannelHashes_TeamsWebhookWithWebhooks(t *testing.T) {
	cfg := config.DefaultConfig()
	// teams_webhook with configured webhooks — this is the real-world
	// scenario where the map type from JSON unmarshal (map[string]any)
	// would cause a panic on the old unchecked vv.(map[string]string)
	settings, _ := json.Marshal(map[string]any{
		"enabled": true,
		"webhooks": map[string]any{
			"hook1": "https://example.com/webhook",
		},
	})
	cfg.Channels["teams_webhook"] = &config.Channel{
		Enabled:  true,
		Type:     config.ChannelTeamsWebHook,
		Settings: config.RawNode(settings),
	}

	assert.NotPanics(t, func() {
		_ = toChannelHashes(cfg)
	})
	h := toChannelHashes(cfg)
	assert.Equal(t, 1, len(h))
	assert.Contains(t, h, "teams_webhook")
}
