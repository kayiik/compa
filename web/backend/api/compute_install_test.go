package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

func TestInstallLinkHandsTheKernelToANewMachineOnce(t *testing.T) {
	mux := computeMux(t)
	kernelBytes := []byte("#!/bin/sh\necho pretend kernel\n")
	kernel := filepath.Join(t.TempDir(), "compa-kernel")
	if err := os.WriteFile(kernel, kernelBytes, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)

	var added struct {
		Token         string `json:"token"`
		EnrollCommand string `json:"enroll_command"`
		ManualCommand string `json:"manual_command"`
	}
	rec := computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	wantLink := "http://compa.test:18800/api/compute/install.sh?token=" + added.Token
	if added.EnrollCommand != "curl -fsSL '"+wantLink+"' | sh" {
		t.Fatalf("the one-liner = %q", added.EnrollCommand)
	}
	if !strings.HasPrefix(added.ManualCommand, "compa-kernel compute enroll --url http://compa.test:18800 --token ") {
		t.Fatalf("the manual command = %q", added.ManualCommand)
	}

	sum := sha256.Sum256(kernelBytes)
	rec = computeCall(mux, "GET", "/api/compute/install.sh?token="+added.Token, "")
	script := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(script, "#!/bin/sh") ||
		!strings.Contains(script, "BASE='http://compa.test:18800'") ||
		!strings.Contains(script, "WANT_SHA='"+hex.EncodeToString(sum[:])+"'") ||
		!strings.Contains(script, "compa-kernel compute enroll") || !strings.Contains(script, "systemctl --user") {
		t.Fatalf("install.sh = %d %s", rec.Code, script)
	}
	if rec := computeCall(mux, "GET", "/api/compute/install.sh?token=wrong", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	if rec := computeCall(mux, "GET", "/api/compute/install.sh", ""); rec.Code != http.StatusForbidden {
		t.Fatalf("no token = %d", rec.Code)
	}

	rec = computeCall(mux, "GET", "/api/compute/kernel?token="+added.Token+"&os="+runtime.GOOS+"&arch="+runtime.GOARCH, "")
	if rec.Code != http.StatusOK || rec.Body.String() != string(kernelBytes) {
		t.Fatalf("kernel = %d %q", rec.Code, rec.Body)
	}
	if rec := computeCall(mux, "GET", "/api/compute/kernel?token="+added.Token+"&os=plan9&arch=mips", ""); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "plan9/mips") {
		t.Fatalf("a kernel for another platform = %d %s", rec.Code, rec.Body)
	}
	if rec := computeCall(mux, "GET", "/api/compute/kernel?token=wrong&os="+runtime.GOOS+"&arch="+runtime.GOARCH, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("kernel with a wrong token = %d", rec.Code)
	}

	// Enrolling uses the token up, and the link dies with it.
	srv := httptest.NewServer(mux)
	defer srv.Close()
	if _, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{}); err != nil {
		t.Fatal(err)
	}
	if rec := computeCall(mux, "GET", "/api/compute/install.sh?token="+added.Token, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("a used token = %d", rec.Code)
	}
}

func TestInstallScriptQuotesWhatItEmbeds(t *testing.T) {
	s := compute.InstallScript("http://h:1/it's", "tok'en", "abc")
	if !strings.Contains(s, `BASE='http://h:1/it'\''s'`) || !strings.Contains(s, `TOKEN='tok'\''en'`) {
		t.Fatalf("a quote in the address or token must not break out of the script:\n%s", s[:400])
	}
}

func TestInstallWithoutAKernelSaysSo(t *testing.T) {
	mux := computeMux(t)
	t.Setenv(config.EnvBinary, filepath.Join(t.TempDir(), "missing"))
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	var added struct{ Token string }
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`).Body.Bytes(), &added)
	rec := computeCall(mux, "GET", "/api/compute/install.sh?token="+added.Token, "")
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "compute enroll") {
		t.Fatalf("no kernel to hand out = %d %s", rec.Code, rec.Body)
	}
}
