package model

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kayiik/compa/cmd/compa-kernel/internal/apikey"
	"github.com/kayiik/compa/cmd/compa-kernel/internal/jsonout"
	"github.com/kayiik/compa/pkg/auth"
	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/modelservice"
)

// instanceFakes stand in for the auth store, the catalog driver and the
// gateway: no test reaches the network or the real auth store.
type instanceFakes struct {
	stored   map[string]*auth.AuthCredential
	synced   modelservice.ProviderCatalogSyncInput
	reloads  int
	reloaded bool
}

func stubInstances(t *testing.T, models ...string) *instanceFakes {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	fakes := &instanceFakes{stored: map[string]*auth.AuthCredential{}, reloaded: true}
	previousDeps, previousReload := instanceDeps, reloadGateway
	instanceDeps = func() modelservice.InstanceDeps {
		return modelservice.InstanceDeps{
			GetCredential: func(key string) (*auth.AuthCredential, error) {
				if cred, ok := fakes.stored[key]; ok {
					return cred, nil
				}
				return nil, errors.New("not found")
			},
			SetCredential: func(key string, cred *auth.AuthCredential) error {
				fakes.stored[key] = cred
				return nil
			},
			DeleteCredential: func(key string) error {
				delete(fakes.stored, key)
				return nil
			},
			ResolveCredential: func(ref string) (string, error) {
				if cred, ok := fakes.stored[strings.TrimPrefix(ref, "credential:")]; ok {
					return cred.AccessToken, nil
				}
				return "", errors.New("no credential")
			},
			SyncCatalog: func(_ context.Context, input modelservice.ProviderCatalogSyncInput) ([]modelservice.CatalogModel, error) {
				fakes.synced = input
				catalog := []modelservice.CatalogModel{}
				for _, model := range models {
					catalog = append(catalog, modelservice.CatalogModel{ID: model})
				}
				return catalog, nil
			},
		}
	}
	reloadGateway = func(context.Context, string) (bool, error) {
		fakes.reloads++
		return fakes.reloaded, nil
	}
	t.Cleanup(func() { instanceDeps, reloadGateway = previousDeps, previousReload })
	return fakes
}

// pipeKey makes stdin, not a terminal, hold key.
func pipeKey(t *testing.T, key string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "stdin")
	require.NoError(t, os.WriteFile(path, []byte(key+"\n"), 0o600))
	f, err := os.Open(path)
	require.NoError(t, err)
	previousInput, previousTerminal := keyInput, keyTerminal
	keyInput = func() *os.File { return f }
	keyTerminal = apikey.Terminal{IsTerminal: func(*os.File) bool { return false }, ReadHidden: apikey.System.ReadHidden}
	t.Cleanup(func() {
		keyInput, keyTerminal = previousInput, previousTerminal
		f.Close()
	})
}

func emptyConfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	t.Setenv(config.EnvConfig, path)
	require.NoError(t, config.SaveConfig(path, config.DefaultConfig()))
	return path
}

func TestInstanceCommandsInheritJSONFlag(t *testing.T) {
	cmd := NewModelCommand()
	for _, name := range []string{"instances", "add", "update", "remove", "catalogs", "models", "sync"} {
		sub, _, err := cmd.Find([]string{name})
		require.NoError(t, err)
		require.Equal(t, name, sub.Name())
		assert.NotNil(t, sub.InheritedFlags().Lookup("json"), name)
	}
}

func TestInstancesJSONListsAreNeverNull(t *testing.T) {
	stubInstances(t)
	emptyConfig(t)
	for args, want := range map[string]string{
		"instances": `{"instances": [], "total": 0}`,
		"catalogs":  `{"catalogs": [], "total": 0}`,
		"models":    `{"targets": [], "total": 0}`,
	} {
		stdout, _, printed, err := runModel(t, args, "--json")
		require.NoError(t, err, args)
		assert.Empty(t, printed)
		assert.JSONEq(t, want, stdout, args)
	}
}

func TestAddJSONReadsTheKeyFromStdinOnly(t *testing.T) {
	fakes := stubInstances(t)
	path := emptyConfig(t)
	pipeKey(t, "sk-secret")

	stdout, stderr, printed, err := runModel(t, "add", "work", "--kind", "openai",
		"--adapter", config.ProviderAdapterOpenAICompatible, "--protocol", "openai",
		"--endpoint", "https://api.example.test/v1", "--header", "X-Team=core",
		"--setting", "display_name=Work", "--api-key-stdin", "--json")
	require.NoError(t, err)
	assert.Empty(t, printed)
	assert.NotContains(t, stdout+stderr, "sk-secret")
	assert.JSONEq(t, `{
		"status": "ok",
		"instance": {
			"id": "work", "display_name": "OpenAI", "provider_kind": "openai",
			"adapter": "openai-compatible", "protocol": "openai", "endpoint": "https://api.example.test/v1",
			"state": "enabled", "credential_configured": true,
			"header_names": ["X-Team"], "setting_names": ["display_name"]
		},
		"reloaded": true
	}`, stdout)
	require.Contains(t, fakes.stored, "provider-work")
	assert.Equal(t, "sk-secret", fakes.stored["provider-work"].AccessToken)
	assert.Equal(t, 1, fakes.reloads)

	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	require.Len(t, cfg.ProviderInstances, 1)
	assert.Equal(t, "credential:provider-work", cfg.ProviderInstances[0].AuthConnectionRef)
	assert.Equal(t, "Work", cfg.ProviderInstances[0].Settings["display_name"])

	// No flag takes the key as an argument.
	_, _, _, err = runModel(t, "add", "other", "--api-key", "sk-arg", "--json")
	require.ErrorContains(t, err, "unknown flag: --api-key")
}

func TestAddJSONFailureIsAnError(t *testing.T) {
	stubInstances(t)
	emptyConfig(t)

	stdout, _, _, err := runModel(t, "add", "Bad ID", "--kind", "openai", "--adapter", "openai-compatible",
		"--protocol", "openai", "--json")
	require.ErrorContains(t, err, "Validation error")
	assert.Empty(t, stdout)
}

func TestUpdateJSONChangesOnlyTheFlagsGiven(t *testing.T) {
	fakes := stubInstances(t)
	path, _ := testConfig(t)
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err)
	cfg.ModelRoutes = nil
	cfg.ProviderInstances[0].Headers = map[string]string{"X-Old": "1"}
	require.NoError(t, config.SaveConfig(path, cfg))
	pipeKey(t, "sk-new")

	stdout, _, _, err := runModel(t, "update", "local", "--header", "X-New=2", "--api-key-stdin", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"status": "ok",
		"instance": {
			"id": "local", "display_name": "local", "provider_kind": "ollama",
			"adapter": "openai-compatible", "protocol": "ollama", "endpoint": "http://127.0.0.1:11434/v1",
			"state": "enabled", "credential_configured": true,
			"header_names": ["X-New", "X-Old"], "setting_names": []
		},
		"reloaded": true
	}`, replaceDisplayName(t, stdout))
	assert.Equal(t, "sk-new", fakes.stored["provider-local"].AccessToken)

	stdout, _, _, err = runModel(t, "update", "local", "--clear-api-key", "--disabled", "--json")
	require.NoError(t, err)
	assert.Contains(t, stdout, `"credential_configured": false`)
	assert.Contains(t, stdout, `"state": "disabled"`)
	assert.NotContains(t, fakes.stored, "provider-local")

	_, _, _, err = runModel(t, "update", "ghost", "--disabled", "--json")
	require.ErrorContains(t, err, `provider instance "ghost" not found`)
}

// replaceDisplayName pins the display name, which comes from the core
// registry for a registry kind.
func replaceDisplayName(t *testing.T, doc string) string {
	t.Helper()
	var view struct {
		Instance struct {
			DisplayName string `json:"display_name"`
		} `json:"instance"`
	}
	decodeOne(t, doc, &view)
	return strings.Replace(doc, `"display_name": "`+view.Instance.DisplayName+`"`, `"display_name": "local"`, 1)
}

func TestRemoveBlockedByRoutesListsThem(t *testing.T) {
	stubInstances(t)
	testConfig(t)

	stdout, _, _, err := runModel(t, "remove", "local", "--json")
	require.Error(t, err)
	assert.Empty(t, stdout)

	var doc bytes.Buffer
	require.NoError(t, jsonout.WriteError(&doc, err))
	assert.JSONEq(t, `{"error": "provider instance \"local\" is referenced by routes: everyday", "routes": ["everyday"]}`, doc.String())
}

func TestRemoveJSON(t *testing.T) {
	fakes := stubInstances(t)
	fakes.reloaded = false
	path, cfg := testConfig(t)
	cfg.ModelRoutes = nil
	require.NoError(t, config.SaveConfig(path, cfg))

	stdout, _, _, err := runModel(t, "remove", "local", "--json")
	require.NoError(t, err)
	assert.JSONEq(t, `{"status": "ok", "instance_id": "local", "reloaded": false}`, stdout)

	_, _, _, err = runModel(t, "remove", "local", "--json")
	require.ErrorContains(t, err, `provider instance "local" not found`)
	var doc bytes.Buffer
	require.NoError(t, jsonout.WriteError(&doc, err))
	assert.JSONEq(t, `{"error": "provider instance \"local\" not found"}`, doc.String())
}

func TestSyncCatalogsAndModelsJSON(t *testing.T) {
	stubInstances(t, "qwen3", "llama3", "phi4")
	testConfig(t)

	stdout, stderr, printed, err := runModel(t, "sync", "local", "--json")
	require.NoError(t, err)
	assert.Empty(t, printed)
	assert.Contains(t, stderr, "Fetching the models of local")
	assert.JSONEq(t, `{
		"instance_id": "local",
		"models": [{"id": "qwen3"}, {"id": "llama3"}, {"id": "phi4"}],
		"total": 3,
		"reloaded": true
	}`, stdout)

	stdout, _, _, err = runModel(t, "catalogs", "--json")
	require.NoError(t, err)
	var catalogs struct {
		Catalogs []struct {
			InstanceID   string                      `json:"instance_id"`
			ProviderKind string                      `json:"provider_kind"`
			Models       []modelservice.CatalogModel `json:"models"`
			FetchedAt    string                      `json:"fetched_at"`
		} `json:"catalogs"`
		Total int `json:"total"`
	}
	decodeOne(t, stdout, &catalogs)
	require.Equal(t, 1, catalogs.Total)
	assert.Equal(t, "local", catalogs.Catalogs[0].InstanceID)
	assert.Equal(t, "ollama", catalogs.Catalogs[0].ProviderKind)
	assert.Len(t, catalogs.Catalogs[0].Models, 3)
	assert.NotEmpty(t, catalogs.Catalogs[0].FetchedAt)

	targetsOf := func(args ...string) []string {
		stdout, _, _, err := runModel(t, append([]string{"models", "--json"}, args...)...)
		require.NoError(t, err)
		var list struct {
			Targets []struct {
				Target     string `json:"target"`
				InstanceID string `json:"instance_id"`
				ModelID    string `json:"model_id"`
				Label      string `json:"label"`
			} `json:"targets"`
			Total int `json:"total"`
		}
		decodeOne(t, stdout, &list)
		require.Len(t, list.Targets, list.Total)
		targets := []string{}
		for _, target := range list.Targets {
			targets = append(targets, target.Target)
		}
		return targets
	}
	assert.Equal(t, []string{"local/llama3", "local/qwen3"}, targetsOf())
	assert.Equal(t, []string{"local/llama3", "local/phi4", "local/qwen3"}, targetsOf("--all"))
	assert.Equal(t, []string{}, targetsOf("--all", "--instance", "other"))

	_, _, _, err = runModel(t, "sync", "ghost", "--json")
	require.ErrorContains(t, err, `provider instance "ghost" not found`)
}

func TestInstancesText(t *testing.T) {
	stubInstances(t)
	testConfig(t)

	stdout, _, _, err := runModel(t, "instances")
	require.NoError(t, err)
	assert.Contains(t, stdout, "ID")
	assert.Contains(t, stdout, "local")
	assert.Contains(t, stdout, "http://127.0.0.1:11434/v1")
}
