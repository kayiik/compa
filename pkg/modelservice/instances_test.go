package modelservice

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kayiik/compa/pkg/auth"
	"github.com/kayiik/compa/pkg/config"
)

// fakeCredentials is an auth store in memory whose writes can fail.
type fakeCredentials struct {
	stored  map[string]*auth.AuthCredential
	setErr  error
	deleted []string
}

func newFakeCredentials() *fakeCredentials {
	return &fakeCredentials{stored: map[string]*auth.AuthCredential{}}
}

func (f *fakeCredentials) deps() InstanceDeps {
	return InstanceDeps{
		GetCredential: func(key string) (*auth.AuthCredential, error) {
			if cred, ok := f.stored[key]; ok {
				return cred, nil
			}
			return nil, errors.New("not found")
		},
		SetCredential: func(key string, cred *auth.AuthCredential) error {
			if f.setErr != nil {
				return f.setErr
			}
			f.stored[key] = cred
			return nil
		},
		DeleteCredential: func(key string) error {
			f.deleted = append(f.deleted, key)
			delete(f.stored, key)
			return nil
		},
		ResolveCredential: func(ref string) (string, error) {
			if cred, ok := f.stored[ref[len("credential:"):]]; ok {
				return cred.AccessToken, nil
			}
			return "", errors.New("no credential")
		},
	}
}

func workInput() InstanceInput {
	return InstanceInput{
		ID: "work", ProviderKind: "openai", Adapter: config.ProviderAdapterOpenAICompatible,
		Protocol: "openai", Endpoint: "https://api.example.test/v1", State: config.ProviderInstanceStateEnabled,
	}
}

func strPtr(s string) *string { return &s }

// workConfig saves a config with the instance "work", its catalog of
// "m1" and "m2", both shortlisted and the route "everyday" naming m1.
func workConfig(t *testing.T) string {
	t.Helper()
	cfg := config.DefaultConfig()
	path := connectFixture(t, cfg)
	if _, err := CreateInstance(path, workInput(), newFakeCredentials().deps()); err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	cfg = mustLoad(t, path)
	if err := SaveProviderInstanceCatalog(instanceByID(cfg, "work"), []CatalogModel{{ID: "m1"}, {ID: "m2"}}); err != nil {
		t.Fatalf("SaveProviderInstanceCatalog() error = %v", err)
	}
	cfg.ActiveModels = []string{"work/m1", "work/m2"}
	cfg.ModelRoutes = []*config.ModelRouteConfig{{Name: "everyday", Targets: []string{"work/m1"}}}
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	return path
}

func TestCreateInstanceStoresTheAPIKey(t *testing.T) {
	path := connectFixture(t, nil)
	creds := newFakeCredentials()
	input := workInput()
	input.APIKey = strPtr("  sk-work  ")

	instance, err := CreateInstance(path, input, creds.deps())
	if err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	if instance.AuthConnectionRef != "credential:provider-work" {
		t.Fatalf("AuthConnectionRef = %q", instance.AuthConnectionRef)
	}
	if cred := creds.stored["provider-work"]; cred == nil || cred.AccessToken != "sk-work" || cred.AuthMethod != APIKeyAuthMethod {
		t.Fatalf("stored credential = %+v", cred)
	}
	if saved := instanceByID(mustLoad(t, path), "work"); saved == nil || saved.AuthConnectionRef != "credential:provider-work" {
		t.Fatalf("saved instance = %+v", saved)
	}

	if _, err := CreateInstance(path, workInput(), creds.deps()); !errors.Is(err, ErrInstanceConflict) {
		t.Fatalf("second CreateInstance() error = %v, want a conflict", err)
	}
}

func TestCreateInstanceRollsBackWhenTheKeyFailsToStore(t *testing.T) {
	path := connectFixture(t, nil)
	creds := newFakeCredentials()
	creds.setErr = errors.New("keyring locked")
	input := workInput()
	input.APIKey = strPtr("sk-work")

	_, err := CreateInstance(path, input, creds.deps())
	if !errors.Is(err, ErrInstanceInternal) || err.Error() != "Failed to save provider credential: keyring locked" {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	if instanceByID(mustLoad(t, path), "work") != nil {
		t.Fatal("the instance stayed in the config")
	}
}

func TestCreateInstanceRefusesInvalidInput(t *testing.T) {
	path := connectFixture(t, nil)
	for name, change := range map[string]func(*InstanceInput){
		"id":       func(in *InstanceInput) { in.ID = "Bad ID" },
		"endpoint": func(in *InstanceInput) { in.Endpoint = "https://u:p@example.test/v1" },
		"clear":    func(in *InstanceInput) { in.ClearHeaders = true },
	} {
		input := workInput()
		change(&input)
		if _, err := CreateInstance(path, input, newFakeCredentials().deps()); !errors.Is(err, ErrInstanceInvalid) {
			t.Errorf("%s: CreateInstance() error = %v, want invalid", name, err)
		}
	}
}

func TestUpdateInstanceRestoresTheConfigWhenTheKeyFailsToStore(t *testing.T) {
	path := workConfig(t)
	creds := newFakeCredentials()
	creds.setErr = errors.New("keyring locked")
	input := workInput()
	input.Endpoint = "https://other.example.test/v1"
	input.APIKey = strPtr("sk-new")

	if _, err := UpdateInstance(path, "work", input, creds.deps()); !errors.Is(err, ErrInstanceInternal) {
		t.Fatalf("UpdateInstance() error = %v", err)
	}
	cfg := mustLoad(t, path)
	if got := instanceByID(cfg, "work"); got.Endpoint != "https://api.example.test/v1" || got.AuthConnectionRef != "" {
		t.Fatalf("instance = %+v, want it as it was", got)
	}
	if !slices.Equal(cfg.ActiveModels, []string{"work/m1", "work/m2"}) {
		t.Fatalf("ActiveModels = %v, want them restored", cfg.ActiveModels)
	}
}

func TestUpdateInstanceNewConnectionDropsTheCatalog(t *testing.T) {
	path := workConfig(t)
	input := workInput()
	input.Endpoint = "https://other.example.test/v1"

	instance, err := UpdateInstance(path, "work", input, newFakeCredentials().deps())
	if err != nil {
		t.Fatalf("UpdateInstance() error = %v", err)
	}
	if instance.Endpoint != input.Endpoint {
		t.Fatalf("Endpoint = %q", instance.Endpoint)
	}
	cfg := mustLoad(t, path)
	if len(cfg.ActiveModels) != 0 || len(cfg.ModelRoutes) != 0 {
		t.Fatalf("ActiveModels = %v, ModelRoutes = %v, want none", cfg.ActiveModels, cfg.ModelRoutes)
	}
	store, err := LoadCatalogs()
	if err != nil {
		t.Fatalf("LoadCatalogs() error = %v", err)
	}
	if len(ListInstanceCatalogs(cfg, store)) != 0 {
		t.Fatal("the catalog stayed")
	}
}

func TestUpdateInstanceRefusals(t *testing.T) {
	path := workConfig(t)
	creds := newFakeCredentials()

	if _, err := UpdateInstance(path, "ghost", InstanceInput{ID: "ghost"}, creds.deps()); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("missing: error = %v", err)
	}
	if _, err := UpdateInstance(path, "work", InstanceInput{ID: "other"}, creds.deps()); !errors.Is(err, ErrInstanceInvalid) {
		t.Fatalf("id mismatch: error = %v", err)
	}
	disable := workInput()
	disable.State = config.ProviderInstanceStateDisabled
	_, err := UpdateInstance(path, "work", disable, creds.deps())
	var instanceErr *InstanceError
	if !errors.Is(err, ErrInstanceConflict) || !errors.As(err, &instanceErr) || !slices.Equal(instanceErr.Routes, []string{"everyday"}) {
		t.Fatalf("disable a routed instance: error = %v", err)
	}
}

func TestDeleteInstance(t *testing.T) {
	path := workConfig(t)
	creds := newFakeCredentials()

	err := DeleteInstance(path, "work", creds.deps())
	var instanceErr *InstanceError
	if !errors.Is(err, ErrInstanceConflict) || !errors.As(err, &instanceErr) ||
		!slices.Equal(instanceErr.Routes, []string{"everyday"}) ||
		err.Error() != `provider instance "work" is referenced by routes: everyday` {
		t.Fatalf("DeleteInstance() of a routed instance error = %v", err)
	}
	if err := DeleteInstance(path, "ghost", creds.deps()); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("DeleteInstance() of a missing instance error = %v", err)
	}

	cfg := mustLoad(t, path)
	cfg.ModelRoutes = nil
	instanceByID(cfg, "work").AuthConnectionRef = "credential:provider-work"
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if err := DeleteInstance(path, "work", creds.deps()); err != nil {
		t.Fatalf("DeleteInstance() error = %v", err)
	}
	cfg = mustLoad(t, path)
	if instanceByID(cfg, "work") != nil || len(cfg.ActiveModels) != 0 {
		t.Fatalf("instances = %v, active = %v", cfg.ProviderInstances, cfg.ActiveModels)
	}
	if !slices.Equal(creds.deleted, []string{"provider-work"}) {
		t.Fatalf("deleted credentials = %v", creds.deleted)
	}
}

func TestSyncInstanceCatalogPrunesVanishedModels(t *testing.T) {
	path := workConfig(t)
	cfg := mustLoad(t, path)
	cfg.ModelRoutes = nil
	cfg.Agents.Defaults.ModelName = "work/m2"
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	deps := newFakeCredentials().deps()
	deps.SyncCatalog = catalogOf("m1", "m3")

	result, err := SyncInstanceCatalog(context.Background(), path, "work", deps)
	if err != nil {
		t.Fatalf("SyncInstanceCatalog() error = %v", err)
	}
	if result.InstanceID != "work" || result.Total != 2 || len(result.Models) != 2 {
		t.Fatalf("result = %+v", result)
	}
	cfg = mustLoad(t, path)
	if !slices.Equal(cfg.ActiveModels, []string{"work/m1"}) || cfg.Agents.Defaults.ModelName != "" {
		t.Fatalf("ActiveModels = %v, default = %q", cfg.ActiveModels, cfg.Agents.Defaults.ModelName)
	}
	store, err := LoadCatalogs()
	if err != nil {
		t.Fatalf("LoadCatalogs() error = %v", err)
	}
	targets := ListProviderTargets(cfg, store)
	if len(targets) != 2 || targets[0].Target != "work/m1" || targets[1].Target != "work/m3" {
		t.Fatalf("targets = %+v", targets)
	}
}

func TestSyncInstanceCatalogPassesTheSecret(t *testing.T) {
	path := connectFixture(t, nil)
	creds := newFakeCredentials()
	input := workInput()
	input.APIKey = strPtr("sk-work")
	if _, err := CreateInstance(path, input, creds.deps()); err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	deps := creds.deps()
	var secret string
	deps.SyncCatalog = func(_ context.Context, in ProviderCatalogSyncInput) ([]CatalogModel, error) {
		secret = in.Secret
		return nil, nil
	}
	result, err := SyncInstanceCatalog(context.Background(), path, "work", deps)
	if err != nil {
		t.Fatalf("SyncInstanceCatalog() error = %v", err)
	}
	if secret != "sk-work" || result.Models == nil || result.Total != 0 {
		t.Fatalf("secret = %q, result = %+v", secret, result)
	}
}

func TestSyncInstanceCatalogRefusals(t *testing.T) {
	path := workConfig(t)
	deps := newFakeCredentials().deps()
	deps.SyncCatalog = catalogOf("m1")

	if _, err := SyncInstanceCatalog(context.Background(), path, "ghost", deps); !errors.Is(err, ErrInstanceNotFound) {
		t.Fatalf("missing: error = %v", err)
	}
	failing := deps
	failing.SyncCatalog = func(context.Context, ProviderCatalogSyncInput) ([]CatalogModel, error) {
		return nil, errors.New("unreachable")
	}
	if _, err := SyncInstanceCatalog(context.Background(), path, "work", failing); !errors.Is(err, ErrInstanceUpstream) {
		t.Fatalf("failing sync: error = %v", err)
	}
	unconfigured := deps
	unconfigured.SyncCatalog = nil
	if _, err := SyncInstanceCatalog(context.Background(), path, "work", unconfigured); !errors.Is(err, ErrInstanceNotConfigured) {
		t.Fatalf("no driver: error = %v", err)
	}

	cfg := mustLoad(t, path)
	cfg.ModelRoutes = nil
	instanceByID(cfg, "work").State = config.ProviderInstanceStateDisabled
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if _, err := SyncInstanceCatalog(context.Background(), path, "work", deps); !errors.Is(err, ErrInstanceDisabled) {
		t.Fatalf("disabled: error = %v", err)
	}

	instance := instanceByID(cfg, "work")
	instance.State = config.ProviderInstanceStateEnabled
	instance.Endpoint = ""
	if err := config.SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	if _, err := SyncInstanceCatalog(context.Background(), path, "work", deps); !errors.Is(err, ErrInstanceUnsupported) {
		t.Fatalf("no endpoint: error = %v", err)
	}
}

func TestListInstancesShowsNoSecrets(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.ProviderInstances = []*config.ProviderInstanceConfig{{
		ID: "work", ProviderKind: "openai", Adapter: config.ProviderAdapterOpenAICompatible, Protocol: "openai",
		AuthConnectionRef: "credential:provider-work", Headers: map[string]string{"X-B": "secret", "X-A": "secret"},
		Runtime: &config.ProviderInstanceRuntime{Proxy: "http://user:pass@proxy.test:8080"},
		State:   config.ProviderInstanceStateEnabled,
	}}
	views := ListInstances(cfg)
	if len(views) != 1 {
		t.Fatalf("views = %+v", views)
	}
	view := views[0]
	if !view.CredentialConfigured || !slices.Equal(view.HeaderNames, []string{"X-A", "X-B"}) ||
		view.SettingNames == nil || view.Runtime.Proxy != "http://user:****@proxy.test:8080" {
		t.Fatalf("view = %+v", view)
	}
	if cfg.ProviderInstances[0].Runtime.Proxy != "http://user:pass@proxy.test:8080" {
		t.Fatal("the view redacted the config's proxy")
	}
	if got := ListInstances(config.DefaultConfig()); got == nil || len(got) != 0 {
		t.Fatalf("ListInstances(empty) = %#v", got)
	}
}
