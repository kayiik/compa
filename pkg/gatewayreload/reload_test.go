package gatewayreload

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kayiik/compa/pkg/config"
	ppid "github.com/kayiik/compa/pkg/pid"
)

func TestReloadWithoutGatewayReloadsNothing(t *testing.T) {
	reloaded, err := Reload(context.Background(), t.TempDir(), config.DefaultConfig())
	if err != nil || reloaded {
		t.Fatalf("Reload() = %v, %v; want false, nil", reloaded, err)
	}
}

func TestReloadPostsWithTheToken(t *testing.T) {
	var method, path, authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, authorization = r.Method, r.URL.Path, r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	reloaded, err := reload(context.Background(), server.URL, "tok")
	if err != nil || !reloaded {
		t.Fatalf("reload() = %v, %v; want true, nil", reloaded, err)
	}
	if method != http.MethodPost || path != "/reload" || authorization != "Bearer tok" {
		t.Fatalf("request = %s %s %q", method, path, authorization)
	}
}

func TestReloadRefused(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	if reloaded, err := reload(context.Background(), server.URL, ""); err == nil || reloaded {
		t.Fatalf("reload() = %v, %v; want false and an error", reloaded, err)
	}
}

func TestBaseURLPrefersThePidFile(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Gateway.Port = 1234
	if got := BaseURL(&ppid.PidFileData{Host: "127.0.0.1", Port: 4321}, cfg); got != "http://127.0.0.1:4321" {
		t.Fatalf("BaseURL() = %q", got)
	}
	if got := BaseURL(&ppid.PidFileData{Host: "127.0.0.1"}, cfg); got != "http://127.0.0.1:1234" {
		t.Fatalf("BaseURL() = %q", got)
	}
}
