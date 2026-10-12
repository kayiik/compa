package docker_test

// The image smoke test: it builds docker/Dockerfile from compa and
// compa-kernel built here for linux, starts it the way an unattended install
// does, and checks that Compa came up behind its password with a running
// gateway. It needs Docker, so it runs only when COMPA_TEST_IMAGE is set:
//
//	COMPA_TEST_IMAGE=1 go test ./docker/
//
// DOCKER_CONTEXT picks the engine; the test copies files in with docker cp
// rather than mounting them, so a remote engine works too. Run it before a
// release. Build the web UI first (pnpm run build:backend), as compa embeds
// it.

import (
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestImageStartsBehindItsPassword(t *testing.T) {
	if os.Getenv("COMPA_TEST_IMAGE") == "" {
		t.Skip("set COMPA_TEST_IMAGE to build and start the image (needs Docker)")
	}
	if _, err := os.Stat("../web/backend/dist/index.html"); err != nil {
		t.Fatal("build the web UI first: pnpm run build:backend in web/frontend")
	}
	arch := os.Getenv("COMPA_TEST_IMAGE_ARCH")
	if arch == "" {
		arch = "amd64"
	}

	// The build context the release gives the Dockerfile.
	context := t.TempDir()
	bin := filepath.Join(context, "bin", "linux_"+arch)
	for pkg, name := range map[string]string{"../cmd/compa-kernel": "compa-kernel", "../web/backend": "compa"} {
		run(t, []string{"CGO_ENABLED=0", "GOOS=linux", "GOARCH=" + arch},
			"go", "build", "-trimpath", "-o", filepath.Join(bin, name), pkg)
	}
	copyFile(t, "Dockerfile", filepath.Join(context, "Dockerfile"))
	copyFile(t, "../LICENSE", filepath.Join(context, "LICENSE"))
	copyFile(t, "../NOTICE", filepath.Join(context, "NOTICE"))
	if err := os.WriteFile(filepath.Join(bin, "THIRD_PARTY_NOTICES"), []byte("smoke test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	name := "compa-smoke-" + strings.ToLower(rand.Text()[:10])
	run(t, nil, "docker", "build", "--platform", "linux/"+arch, "-t", name, context)
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", name).Run() })

	// Started as an unattended install is: the first password from a file,
	// and a name it is reached by.
	password := "smoke-password-123"
	pwFile := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(pwFile, []byte(password+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, nil, "docker", "create", "--name", name,
		"-e", "COMPA_DASHBOARD_PASSWORD_FILE=/tmp/password",
		"-e", "COMPA_ALLOWED_HOSTS=compa.example", name)
	t.Cleanup(func() {
		if t.Failed() {
			out, _ := exec.Command("docker", "logs", "--tail", "40", name).CombinedOutput()
			t.Logf("container log:\n%s", out)
		}
		_ = exec.Command("docker", "rm", "-f", name).Run()
	})
	run(t, nil, "docker", "cp", pwFile, name+":/tmp/password")
	run(t, nil, "docker", "start", name)

	// curl inside the container: the image ships it for skills.
	curl := func(args ...string) string {
		out, err := exec.Command("docker", append([]string{"exec", name, "curl", "-s", "-m", "10"}, args...)...).CombinedOutput()
		if err != nil {
			return "error: " + err.Error() + ": " + string(out)
		}
		return string(out)
	}
	status := waitFor(t, 60*time.Second, func() (string, bool) {
		out := curl("http://localhost:18800/api/auth/status")
		return out, strings.Contains(out, `"initialized":true`)
	})
	t.Logf("auth status: %s", status)

	if out := curl("-o", "/dev/null", "-w", "%{http_code}", "-H", "Host: unknown.example", "http://localhost:18800/api/auth/status"); out != "421" {
		t.Errorf("an unknown host got %s, want 421", out)
	}
	if out := curl("-o", "/dev/null", "-w", "%{http_code}", "-H", "Host: compa.example", "http://localhost:18800/api/auth/status"); out != "200" {
		t.Errorf("COMPA_ALLOWED_HOSTS's name got %s, want 200", out)
	}

	login := curl("-c", "/tmp/cookies", "-H", "Origin: http://localhost:18800", "-H", "Content-Type: application/json",
		"-d", fmt.Sprintf(`{"password":%q}`, password), "http://localhost:18800/api/auth/login")
	if !strings.Contains(login, `"ok"`) {
		t.Fatalf("sign-in with the file's password: %s", login)
	}
	waitFor(t, 60*time.Second, func() (string, bool) {
		out := curl("-b", "/tmp/cookies", "http://localhost:18800/api/gateway/status")
		return out, strings.Contains(out, `"gateway_status":"running"`)
	})

	if out, _ := exec.Command("docker", "exec", name, "id", "-u").Output(); strings.TrimSpace(string(out)) != "10001" {
		t.Errorf("runs as uid %q, want 10001", strings.TrimSpace(string(out)))
	}
}

func run(t *testing.T, env []string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Env = append(os.Environ(), env...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	data, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// waitFor polls check until it reports done or the limit passes.
func waitFor(t *testing.T, limit time.Duration, check func() (string, bool)) string {
	t.Helper()
	deadline := time.Now().Add(limit)
	var last string
	for time.Now().Before(deadline) {
		out, done := check()
		if done {
			return out
		}
		last = out
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("gave up after %s; last answer: %s", limit, last)
	return ""
}
