package compute

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultImage is the container image used for container isolation. Build it
// with docker/Dockerfile.compita.
const DefaultImage = "compa-compita:local"

// containerHome is where a Compita's directory is mounted in its container.
const containerHome = "/data"

// Peer tells a Compita turn how to reach the Compa host and its other Compitas.
type Peer struct {
	URL        string
	ComputeID  string
	Credential string
}

// Turn is one message to a Compita.
type Turn struct {
	CompitaID string
	Isolation Isolation
	Session   string
	Message   string
	// Depth counts hops of Compita-to-Compita messages, to stop loops.
	Depth int
	// Peer* let a dial-in compute's Compitas reach back to the Compa host.
	PeerURL, PeerCompute, PeerToken string
	// Approvals is the Compita's approval preset (see ApprovalsCareful).
	Approvals string
	// Browser runs the turn in the browser image, with a browser to drive.
	Browser bool
	// Connections are the Compita's own MCP servers.
	Connections map[string]Connection
	// Events asks the kernel to report the turn as it runs (`agent --events`).
	// RunStream sets it; callers of Run leave it off.
	Events bool
}

// Runner runs one Compita turn on this machine. Every Compita has its own
// COMPA_HOME (config, sessions, workspace) under Home/compitas/<id>.
type Runner struct {
	// Home is this machine's Compa home; its config.json seeds each Compita.
	Home string
	// Kernel is the compa-kernel binary used outside containers.
	Kernel string
	// Image is the container image for container isolation.
	Image string
	// BrowserImage is the image for Compitas with a browser.
	BrowserImage string
	// Peer lets the Compita message its peers. Optional.
	Peer *Peer
}

// seedFiles are copied from the machine home into each Compita home.
var seedFiles = []string{"config.json", "model_catalogs.json", "auth.json", ".security.yml"}

// CompitaHome is the directory a Compita owns on this machine.
func (r Runner) CompitaHome(id string) string {
	return filepath.Join(r.Home, "compitas", id)
}

func (r Runner) prepare(t Turn) (string, error) {
	id := t.CompitaID
	if !validID(id) {
		return "", fmt.Errorf("compute: %q is not a Compita id", id)
	}
	dir := r.CompitaHome(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	// A Compita can write its folder (a container mounts it), so a link it
	// leaves there must not make Compa write a file elsewhere: everything
	// Compa writes in the folder goes through root, which stays inside it.
	root, err := os.OpenRoot(dir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if t.Browser {
		prepareBrowserProfile(root)
	}
	// A Compita's model setup follows this machine's whenever it changes:
	// config, model catalogs and provider credentials.
	for _, name := range seedFiles {
		src := filepath.Join(r.Home, name)
		si, err := os.Stat(src)
		if err != nil {
			continue
		}
		var data []byte
		same := bytes.Equal
		switch name {
		case "config.json":
			data, err = r.compitaConfig(src, dir, t)
			same = sameJSON
		case ".security.yml":
			data, err = compitaSecurity(src)
		default:
			// Copied as it is, when the machine's is newer.
			if di, statErr := root.Lstat(name); statErr == nil && di.Mode().IsRegular() && !si.ModTime().After(di.ModTime()) {
				continue
			}
			data, err = os.ReadFile(src)
		}
		if err != nil {
			return "", err
		}
		if err := seedFile(root, name, data, same); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// seedFile makes name in the Compita's folder hold data, unless it already
// does (same says whether two contents are the same). A link or anything else
// the Compita left at that name is removed, not followed.
func seedFile(root *os.Root, name string, data []byte, same func(a, b []byte) bool) error {
	switch fi, err := root.Lstat(name); {
	case err != nil:
		// Not there yet.
	case fi.Mode().IsRegular():
		if old, err := root.ReadFile(name); err == nil && same(old, data) {
			return nil
		}
	default:
		if err := root.RemoveAll(name); err != nil {
			return err
		}
	}
	return root.WriteFile(name, data, 0o600)
}

// prepareBrowserProfile readies the Compita's persistent browser profile for a
// turn. A browser killed with its turn (the container is removed, or killed)
// leaves two things behind:
//   - Chromium's singleton lock. The machine runs one turn at a time per
//     Compita, so a lock found here is stale, and a stale one makes the next
//     turn's browser refuse to start ("Browser is already in use").
//   - a "crashed" exit mark, which makes Chromium offer to restore pages in a
//     bubble over the page: noise in the live view, every turn.
func prepareBrowserProfile(root *os.Root) {
	profile := "browser-profile"
	for _, name := range []string{"SingletonLock", "SingletonCookie", "SingletonSocket"} {
		_ = root.Remove(filepath.Join(profile, name))
	}
	prefs := filepath.Join(profile, "Default", "Preferences")
	if raw, err := root.ReadFile(prefs); err == nil {
		fixed := bytes.ReplaceAll(raw, []byte(`"exit_type":"Crashed"`), []byte(`"exit_type":"Normal"`))
		if !bytes.Equal(fixed, raw) {
			_ = root.WriteFile(prefs, fixed, 0o600)
		}
	}
}

// compitaConfig derives the Compita's config from the machine's at src (see
// buildCompitaConfig).
func (r Runner) compitaConfig(src, dir string, t Turn) ([]byte, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	patch := configPatch{Approvals: t.Approvals, Container: t.Isolation == IsolationContainer, Browser: t.Browser, Connections: t.Connections}
	patch.Home = dir
	patch.Workspace = filepath.Join(patch.Home, "workspace")
	if patch.Container {
		// The Compita's directory is mounted at /data, a path of the
		// container's own, whatever the machine's separator is.
		patch.Home, patch.Workspace = containerHome, path.Join(containerHome, "workspace")
	}
	if r.Peer != nil {
		kernel := r.Kernel
		if kernel == "" || patch.Container {
			kernel = "compa-kernel"
		}
		patch.HookCommand = []string{kernel, "compute", "approver"}
	}
	return buildCompitaConfig(data, patch)
}

// compitaSecurity derives the Compita's .security.yml from the machine's at
// src (see buildCompitaSecurity).
func compitaSecurity(src string) ([]byte, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	return buildCompitaSecurity(data)
}

// command builds the process that answers message. Container isolation runs
// the kernel inside Docker with only the Compita's own directory mounted.
func (r Runner) command(ctx context.Context, t Turn) (*exec.Cmd, error) {
	id, iso := t.CompitaID, t.Isolation
	dir, err := r.prepare(t)
	if err != nil {
		return nil, err
	}
	args := []string{"agent", "-s", t.Session, "-m", t.Message}
	if t.Events {
		args = append(args, "--events")
	}
	var env []string
	if r.Peer != nil {
		url := r.Peer.URL
		if iso == IsolationContainer {
			// Inside a container, the host's loopback is host.docker.internal.
			url = strings.NewReplacer("//127.0.0.1", "//host.docker.internal", "//localhost", "//host.docker.internal").Replace(url)
		}
		env = []string{
			"COMPA_PEER_URL=" + url, "COMPA_PEER_COMPUTE=" + r.Peer.ComputeID,
			"COMPA_PEER_TOKEN=" + r.Peer.Credential, "COMPA_PEER_SELF=" + id,
			"COMPA_PEER_DEPTH=" + strconv.Itoa(t.Depth),
		}
	}
	if iso == IsolationContainer {
		image := r.Image
		if image == "" {
			image = DefaultImage
		}
		if t.Browser {
			image = r.BrowserImage
			if image == "" {
				image = DefaultBrowserImage
			}
		}
		name := fmt.Sprintf("compa-compita-%s-%d", id, time.Now().UnixNano())
		dargs := []string{"run", "--rm", "--name", name, "--label", compitaLabel + "=" + id,
			"--add-host", "host.docker.internal:host-gateway"}
		if uid := os.Getuid(); uid >= 0 {
			// What the container writes in the Compita's directory belongs to
			// its owner. Windows has no user ids, and Docker Desktop needs none.
			dargs = append(dargs, "--user", fmt.Sprintf("%d:%d", uid, os.Getgid()))
		}
		dargs = append(dargs,
			// A Compita must not starve the machine or gain privileges.
			"--memory", memory(t.Browser), "--cpus", containerCPUs, "--pids-limit", containerPids,
			"--cap-drop", "ALL", "--security-opt", "no-new-privileges",
			"-v", dir+":"+containerHome, "-e", "COMPA_HOME="+containerHome, "-e", "COMPA_LOG_FILE="+containerHome+"/compita.log")
		if t.Browser {
			// Chromium needs shared memory; a free host port, on loopback
			// only, shows the browser to a human through noVNC.
			dargs = append(dargs, "--shm-size", "1g", "-p", "127.0.0.1::"+NoVNCPort)
		}
		for _, e := range env {
			dargs = append(dargs, "-e", e)
		}
		cmd := exec.CommandContext(ctx, "docker", append(append(dargs, image), args...)...)
		// Killing the docker client alone would leave the container running.
		cmd.Cancel = func() error {
			_ = exec.Command("docker", "rm", "-f", name).Run()
			return cmd.Process.Kill()
		}
		cmd.WaitDelay = 10 * time.Second
		return cmd, nil
	}
	kernel := r.Kernel
	if kernel == "" {
		kernel = "compa-kernel"
	}
	cmd := exec.CommandContext(ctx, kernel, args...)
	cmd.Env = append(append(os.Environ(), "COMPA_HOME="+dir, "COMPA_LOG_FILE="+filepath.Join(dir, "compita.log")), env...)
	return cmd, nil
}

// compitaLabel marks the containers of a Compita so stale ones can be found.
const compitaLabel = "compa.compita"

// Limits on a Compita's container. A browser needs more memory.
const (
	containerCPUs = "2"
	containerPids = "1024"
)

func memory(browser bool) string {
	if browser {
		return "3g"
	}
	return "2g"
}

// removeContainers removes any container left from an earlier turn of this
// Compita, such as after the launcher was killed. The host runs one turn at a
// time per Compita, so none of them is live.
func removeContainers(id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-aq", "--filter", "label="+compitaLabel+"="+id).Output()
	if err != nil {
		return
	}
	if ids := strings.Fields(string(out)); len(ids) > 0 {
		_ = exec.CommandContext(ctx, "docker", append([]string{"rm", "-f"}, ids...)...).Run()
	}
}

// compitaLocks makes a machine run one turn at a time per Compita, whoever
// asked: a turn the host gave up on (it restarted) may still be running here.
var compitaLocks sync.Map

func lockCompita(id string) (unlock func()) {
	mu, _ := compitaLocks.LoadOrStore(id, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	return mu.(*sync.Mutex).Unlock
}
