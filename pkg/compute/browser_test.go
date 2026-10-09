package compute

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowserTurnsRunInTheBrowserImageWithALiveViewPort(t *testing.T) {
	r := Runner{Home: t.TempDir()}
	join := func(t Turn) string {
		cmd, err := r.command(context.Background(), t)
		if err != nil {
			panic(err)
		}
		return strings.Join(cmd.Args, " ")
	}
	plain := join(Turn{CompitaID: "ana", Isolation: IsolationContainer, Session: "s", Message: "hi"})
	if strings.Contains(plain, "--shm-size") || strings.Contains(plain, "6080") || !strings.Contains(plain, DefaultImage) {
		t.Fatalf("a Compita without a browser must not get one: %s", plain)
	}
	for _, want := range []string{"--memory 2g", "--cpus 2", "--pids-limit 1024", "--cap-drop ALL", "--security-opt no-new-privileges"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("a Compita's container must be limited, missing %q: %s", want, plain)
		}
	}
	// What the container writes in the Compita's directory belongs to its owner.
	// Windows has no user ids, so there the flag is left out.
	if uid := os.Getuid(); uid >= 0 {
		if want := fmt.Sprintf("--user %d:%d", uid, os.Getgid()); !strings.Contains(plain, want) {
			t.Fatalf("the container must run as the directory's owner, missing %q: %s", want, plain)
		}
	} else if strings.Contains(plain, "--user") {
		t.Fatalf("a machine without user ids must not pass --user: %s", plain)
	}
	web := join(Turn{CompitaID: "ana", Isolation: IsolationContainer, Session: "s", Message: "hi", Browser: true})
	for _, want := range []string{DefaultBrowserImage, "--shm-size 1g", "-p 127.0.0.1::6080", "--memory 3g"} {
		if !strings.Contains(web, want) {
			t.Fatalf("browser turn missing %q: %s", want, web)
		}
	}
	if strings.Contains(web, "-p 0.0.0.0") || strings.Contains(web, "-p 6080") {
		t.Fatalf("the live view must be published on loopback only: %s", web)
	}
}

func TestCompitaBrowserNeedsAContainerOrABrowserOnTheCompute(t *testing.T) {
	s := testStore(t, true)
	if err := s.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	shared, _ := s.AddCompita("a", "", LocalID, IsolationShared)
	if err := s.SetCompitaBrowser(shared.ID, true); err == nil {
		t.Fatal("a browser must be refused outside a container on a compute that has none")
	}
	if err := s.CheckBrowser(LocalID, IsolationShared); err == nil {
		t.Fatal("CheckBrowser must refuse what SetCompitaBrowser refuses")
	}
	if err := s.CheckBrowser(LocalID, IsolationContainer); err != nil {
		t.Fatalf("a container brings its own browser: %v", err)
	}
	if err := s.CheckBrowser("nowhere", IsolationContainer); err != ErrNotFound {
		t.Fatalf("unknown compute = %v", err)
	}
	boxed, err := s.AddCompita("b", "", LocalID, IsolationContainer)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCompitaBrowser(boxed.ID, true); err != nil {
		t.Fatal(err)
	}
	st, _ := s.Load()
	got := false
	for _, c := range st.Compitas {
		if c.ID == boxed.ID {
			got = c.Browser
		}
	}
	if !got {
		t.Fatal("the browser flag must be stored")
	}
	if err := s.SetCompitaBrowser("nobody", true); err != ErrNotFound {
		t.Fatalf("unknown Compita = %v", err)
	}

	// A compute that has a browser of its own (the browser image) gives one at any level.
	s.Detect = func() Capabilities { return Capabilities{Docker: true, Browser: true} }
	if err := s.RefreshLocal(); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCompitaBrowser(shared.ID, true); err != nil {
		t.Fatalf("a browser on a compute that has one: %v", err)
	}
}

func TestBrowserServerFollowsWhereItRuns(t *testing.T) {
	args := func(m map[string]any) string {
		var out []string
		for _, a := range m["args"].([]any) {
			out = append(out, a.(string))
		}
		return strings.Join(out, " ")
	}
	boxed := browserMCPServer(true, "/data")
	if a := args(boxed); !strings.Contains(a, "--no-sandbox") || !strings.Contains(a, "--user-data-dir /data/browser-profile") {
		t.Fatalf("in a container: %s", a)
	}
	if env := boxed["env"].(map[string]any); env["DISPLAY"] != ":99" || env["PLAYWRIGHT_BROWSERS_PATH"] != "/ms-playwright" {
		t.Fatalf("the container's display and browsers: %v", env)
	}

	t.Setenv("COMPA_BROWSER_NO_SANDBOX", "")
	home := t.TempDir()
	bare := browserMCPServer(false, home)
	if a := args(bare); strings.Contains(a, "--no-sandbox") || !strings.Contains(a, "--user-data-dir "+filepath.Join(home, "browser-profile")) {
		t.Fatalf("on a machine: %s", a)
	}
	if _, has := bare["env"]; has {
		t.Fatal("on a machine the server must inherit the machine's own display and browsers")
	}
	t.Setenv("COMPA_BROWSER_NO_SANDBOX", "1")
	if a := args(browserMCPServer(false, home)); !strings.Contains(a, "--no-sandbox") {
		t.Fatalf("a machine that is itself a sandbox opts out: %s", a)
	}
}

func TestBrowserOutsideAContainerKeepsItsProfileInTheCompitasOwnDirectory(t *testing.T) {
	r := Runner{Home: t.TempDir()}
	if err := os.WriteFile(filepath.Join(r.Home, "config.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationProfile, Session: "s", Message: "hi", Browser: true})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Tools struct {
			MCP struct {
				Servers map[string]struct {
					Args []string `json:"args"`
				} `json:"servers"`
			} `json:"mcp"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		t.Fatal(err)
	}
	args := strings.Join(cfg.Tools.MCP.Servers[BrowserServerName].Args, " ")
	if !strings.Contains(args, filepath.Join(dir, "browser-profile")) {
		t.Fatalf("the profile must live in the Compita's directory %s: %s", dir, args)
	}
	if strings.Contains(args, "/data/") {
		t.Fatalf("a Compita outside a container has no /data: %s", args)
	}
}

// A browser killed with its turn leaves Chromium's singleton lock in the
// persistent profile; the next turn's browser then refuses to start ("Browser
// is already in use"). It also leaves a "crashed" exit mark, which makes
// Chromium show a restore bubble in the live view. Found by running browser
// turns in a row.
func TestBrowserTurnClearsWhatAKilledBrowserLeft(t *testing.T) {
	r := Runner{Home: t.TempDir()}
	profile := filepath.Join(r.CompitaHome("ana"), "browser-profile")
	if err := os.MkdirAll(filepath.Join(profile, "Default"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"SingletonLock": "df38dd4d54dd-54", "SingletonCookie": "3793249280474509245",
		"SingletonSocket": "/tmp/org.chromium.gone/SingletonSocket",
	} {
		p := filepath.Join(profile, name)
		if err := os.Symlink(target, p); err != nil {
			// Windows needs a privilege to make a symlink; a plain file is removed the same way.
			if err := os.WriteFile(p, []byte(target), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	prefs := filepath.Join(profile, "Default", "Preferences")
	crashed := `{"profile":{"exit_type":"Crashed","name":"Person 1"},"intl":{"selected_languages":"en-US"}}`
	if err := os.WriteFile(prefs, []byte(crashed), 0o600); err != nil {
		t.Fatal(err)
	}

	// A turn without a browser leaves the profile alone.
	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(profile, "SingletonLock")); err != nil {
		t.Fatalf("a turn without a browser must not touch the profile: %v", err)
	}

	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationContainer, Browser: true}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		if _, err := os.Lstat(filepath.Join(profile, name)); !os.IsNotExist(err) {
			t.Fatalf("%s must be cleared before a browser turn: %v", name, err)
		}
	}
	want := `{"profile":{"exit_type":"Normal","name":"Person 1"},"intl":{"selected_languages":"en-US"}}`
	if b, err := os.ReadFile(prefs); err != nil || string(b) != want {
		t.Fatalf("only the exit mark changes, the rest of the profile stays: %q, %v", b, err)
	}
}
