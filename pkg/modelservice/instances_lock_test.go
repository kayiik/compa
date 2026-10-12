package modelservice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/kayiik/compa/pkg/config"
)

// Concurrent creates and updates take turns on the instance lock, so none
// loses another's write to the config.
func TestConcurrentCreateAndUpdateKeepEveryWrite(t *testing.T) {
	path := connectFixture(t, nil)
	const n = 6
	for i := 0; i < n; i++ {
		input := workInput()
		input.ID = fmt.Sprintf("old%d", i)
		if _, err := CreateInstance(path, input, newFakeCredentials().deps()); err != nil {
			t.Fatalf("CreateInstance(%s) error = %v", input.ID, err)
		}
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			input := workInput()
			input.ID = fmt.Sprintf("new%d", i)
			_, err := CreateInstance(path, input, newFakeCredentials().deps())
			errs <- err
		}(i)
		go func(i int) {
			defer wg.Done()
			input := workInput()
			input.ID = fmt.Sprintf("old%d", i)
			input.Endpoint = fmt.Sprintf("https://updated%d.example.test/v1", i)
			_, err := UpdateInstance(path, input.ID, input, newFakeCredentials().deps())
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent write error = %v", err)
		}
	}

	cfg := mustLoad(t, path)
	for i := 0; i < n; i++ {
		if instanceByID(cfg, fmt.Sprintf("new%d", i)) == nil {
			t.Errorf("created instance new%d was lost", i)
		}
		old := instanceByID(cfg, fmt.Sprintf("old%d", i))
		if want := fmt.Sprintf("https://updated%d.example.test/v1", i); old == nil || old.Endpoint != want {
			t.Errorf("update of old%d was lost: %+v", i, old)
		}
	}
}

// The instance lock is a sidecar in the Compa home, distinct from the
// config's and the catalogs' locks.
func TestInstanceLockPathIsInTheHome(t *testing.T) {
	path := connectFixture(t, nil)
	lock := InstanceLockPath()
	if lock == path || lock == CatalogFilePath() {
		t.Fatalf("InstanceLockPath() = %q shares a lock with the files it guards", lock)
	}
	if _, err := CreateInstance(path, workInput(), newFakeCredentials().deps()); err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	if _, err := os.Stat(lock + ".flock"); err != nil {
		t.Fatalf("lock file: %v", err)
	}
}

// A sync applies its catalog to the config as it is after the fetch: what
// another writer saved during the fetch stays.
func TestSyncAppliesToTheConfigChangedDuringTheFetch(t *testing.T) {
	path := workConfig(t)
	deps := newFakeCredentials().deps()
	deps.SyncCatalog = func(context.Context, ProviderCatalogSyncInput) ([]CatalogModel, error) {
		cfg := mustLoad(t, path)
		cfg.ModelRoutes = append(cfg.ModelRoutes, &config.ModelRouteConfig{Name: "late", Targets: []string{"work/m1"}})
		cfg.ModelRoutes = append(cfg.ModelRoutes, &config.ModelRouteConfig{Name: "gone", Targets: []string{"work/m2"}})
		if err := config.SaveConfig(path, cfg); err != nil {
			return nil, err
		}
		return []CatalogModel{{ID: "m1"}}, nil
	}

	if _, err := SyncInstanceCatalog(context.Background(), path, "work", deps); err != nil {
		t.Fatalf("SyncInstanceCatalog() error = %v", err)
	}
	cfg := mustLoad(t, path)
	var routes []string
	for _, route := range cfg.ModelRoutes {
		routes = append(routes, route.Name)
	}
	if !slices.Equal(routes, []string{"everyday", "late"}) {
		t.Fatalf("routes = %v, want everyday and the route added during the fetch, without the one naming m2", routes)
	}
	if !slices.Equal(cfg.ActiveModels, []string{"work/m1"}) {
		t.Fatalf("ActiveModels = %v", cfg.ActiveModels)
	}
}

// A sync whose instance got a new connection during the fetch saves nothing.
func TestSyncRefusesAnInstanceChangedDuringTheFetch(t *testing.T) {
	path := workConfig(t)
	deps := newFakeCredentials().deps()
	deps.SyncCatalog = func(context.Context, ProviderCatalogSyncInput) ([]CatalogModel, error) {
		cfg := mustLoad(t, path)
		instanceByID(cfg, "work").Endpoint = "https://other.example.test/v1"
		if err := config.SaveConfig(path, cfg); err != nil {
			return nil, err
		}
		return []CatalogModel{{ID: "x"}}, nil
	}
	if _, err := SyncInstanceCatalog(context.Background(), path, "work", deps); !errors.Is(err, ErrInstanceConflict) {
		t.Fatalf("SyncInstanceCatalog() error = %v, want a conflict", err)
	}
	if cfg := mustLoad(t, path); !slices.Equal(cfg.ActiveModels, []string{"work/m1", "work/m2"}) {
		t.Fatalf("ActiveModels = %v", cfg.ActiveModels)
	}
}

// An update that cannot read the stored key the instance uses changes
// nothing, so no rollback can delete that key.
func TestUpdateAbortsWhenTheStoredKeyCannotBeRead(t *testing.T) {
	path := connectFixture(t, nil)
	creds := newFakeCredentials()
	input := workInput()
	input.APIKey = strPtr("sk-old")
	if _, err := CreateInstance(path, input, creds.deps()); err != nil {
		t.Fatalf("CreateInstance() error = %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	creds.getErr = errors.New("auth store unreadable")
	update := workInput()
	update.APIKey = strPtr("sk-new")
	update.Endpoint = "https://new.example.test/v1"
	_, err = UpdateInstance(path, "work", update, creds.deps())
	if !errors.Is(err, ErrInstanceInternal) {
		t.Fatalf("UpdateInstance() error = %v, want an internal error", err)
	}
	if cred := creds.stored["provider-work"]; cred == nil || cred.AccessToken != "sk-old" {
		t.Fatalf("stored credential = %+v, want sk-old untouched", cred)
	}
	if len(creds.deleted) != 0 {
		t.Fatalf("deleted = %v", creds.deleted)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the config changed")
	}
}
