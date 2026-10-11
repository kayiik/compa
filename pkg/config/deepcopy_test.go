package config

import (
	"reflect"
	"testing"
)

func TestDeepCopySharesNothingWithTheOriginal(t *testing.T) {
	cfg := DefaultConfig()
	if err := InitChannelList(cfg.Channels); err != nil {
		t.Fatal(err)
	}
	cfg.Tools.MCP.Servers["github"] = MCPServerConfig{Env: map[string]string{"TOKEN": "a"}}
	cfg.Tools.Web.Brave.SetAPIKey("brave-a")

	cp := deepCopy(reflect.ValueOf(cfg).Elem()).Addr().Interface().(*Config)
	cp.Gateway.Port = 1
	cp.Tools.Skills.Registries[0].BaseURL = "https://changed.example"
	cp.Tools.MCP.Servers["github"].Env["TOKEN"] = "b"
	cp.Tools.Web.Brave.APIKeys[0].Set("brave-b")
	cp.Channels["slack"].Enabled = true
	decoded, err := cp.Channels["slack"].GetDecoded()
	if err != nil {
		t.Fatal(err)
	}
	decoded.(*SlackSettings).BotToken.Set("xoxb-b")

	if cfg.Gateway.Port == 1 || cfg.Tools.Skills.Registries[0].BaseURL == "https://changed.example" {
		t.Fatal("the copy shares scalars or registries with the original")
	}
	if cfg.Tools.MCP.Servers["github"].Env["TOKEN"] != "a" || cfg.Tools.Web.Brave.APIKey() != "brave-a" {
		t.Fatal("the copy shares maps or secure strings with the original")
	}
	original, _ := cfg.Channels["slack"].GetDecoded()
	if cfg.Channels["slack"].Enabled || original.(*SlackSettings).BotToken.String() != "" {
		t.Fatal("the copy shares channels or their decoded settings with the original")
	}
}
