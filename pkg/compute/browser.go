package compute

import (
	"os"
	"path"
	"path/filepath"
)

// DefaultBrowserImage is the image for Compitas with a browser. Build it with
// docker/Dockerfile.compita-browser, after the default image.
const DefaultBrowserImage = "compa-compita-browser:local"

// NoVNCPort is where the browser image serves its live view (websockify, the
// WebSocket side of the display's VNC server), inside the container. The
// machine publishes it on a free loopback port; see LiveAddr.
const NoVNCPort = "6080"

// BrowserServerName names the MCP server that drives the browser; the agent
// sees its tools as mcp_browser_<tool>.
const BrowserServerName = "browser"

// browserMCPServer is the Playwright MCP server entry for a Compita's config.
// The browser is headed on a virtual display, so a human watching through the
// live view sees what the agent does, and its profile lives in the Compita's
// own directory (home, as the kernel sees it), so logins last between turns.
//
// In a container the image sets the display up. Outside one the server
// inherits the machine's own environment (display, browser location); only a
// machine that says so goes without Chromium's sandbox, as the browser image
// does, since there the container around the compute is the sandbox.
func browserMCPServer(container bool, home string) map[string]any {
	join := filepath.Join
	if container {
		join = path.Join // a path inside the container, whatever the machine's separator is
	}
	args := []any{"--browser", "chromium"}
	if container || os.Getenv("COMPA_BROWSER_NO_SANDBOX") == "1" {
		args = append(args, "--no-sandbox")
	}
	args = append(args,
		"--user-data-dir", join(home, "browser-profile"),
		"--output-dir", join(home, "browser-output"),
		"--viewport-size", "1280x800",
	)
	server := map[string]any{"enabled": true, "command": "playwright-mcp", "args": args}
	if container {
		server["env"] = map[string]any{
			"DISPLAY": ":99", "HOME": join(home, "browser-home"), "PLAYWRIGHT_BROWSERS_PATH": "/ms-playwright",
		}
	}
	return server
}
