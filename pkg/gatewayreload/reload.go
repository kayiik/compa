// Package gatewayreload asks a running gateway to reload its config, so a
// program that changed the config outside the launcher, such as
// compa-kernel, can have it applied without a restart.
package gatewayreload

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/kayiik/compa/pkg/config"
	"github.com/kayiik/compa/pkg/netbind"
	ppid "github.com/kayiik/compa/pkg/pid"
)

// Timeout bounds the wait for the gateway's /reload, which answers once the
// reload is done.
const Timeout = 30 * time.Second

// defaultPort is the gateway's port when neither its PID file nor the config
// names one.
const defaultPort = 18790

// Reload asks the gateway the PID file in home describes to reload its
// config, the one cfg was read from, and waits until it has. It reports
// whether a gateway reloaded; no running gateway is false with no error.
func Reload(ctx context.Context, home string, cfg *config.Config) (bool, error) {
	pidData := ppid.ReadPidFileWithCheck(home)
	if pidData == nil {
		return false, nil
	}
	return reload(ctx, BaseURL(pidData, cfg), pidData.Token)
}

// BaseURL is the address of the gateway pidData describes: its host and
// port, else the config's, else a loopback host and the default port.
func BaseURL(pidData *ppid.PidFileData, cfg *config.Config) string {
	port := pidData.Port
	if port == 0 {
		port = defaultPort
		if cfg != nil && cfg.Gateway.Port != 0 {
			port = cfg.Gateway.Port
		}
	}
	host := probeHost(strings.TrimSpace(pidData.Host))
	if host == "" && cfg != nil {
		host = probeHost(strings.TrimSpace(cfg.Gateway.Host))
	}
	if host == "" {
		host = netbind.ResolveAdaptiveLoopbackHost()
	}
	return "http://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func probeHost(bindHost string) string {
	if bindHost == "" {
		return ""
	}
	plan, err := netbind.BuildPlan(bindHost, netbind.DefaultLoopback)
	if err != nil || strings.TrimSpace(plan.ProbeHost) == "" {
		return netbind.ResolveAdaptiveLoopbackHost()
	}
	return plan.ProbeHost
}

func reload(ctx context.Context, baseURL, token string) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/reload", http.NoBody)
	if err != nil {
		return false, err
	}
	if token = strings.TrimSpace(token); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{
		Transport:     &http.Transport{Proxy: nil},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	response, err := client.Do(request)
	if err != nil {
		return false, fmt.Errorf("reach the gateway: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("the gateway answered the reload with HTTP %d", response.StatusCode)
	}
	return true, nil
}
