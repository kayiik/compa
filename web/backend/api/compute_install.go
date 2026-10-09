package api

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"runtime"
	"sync"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/web/backend/utils"
)

func (h *Handler) registerInstallRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/compute/install.sh", h.handleInstallScript)
	mux.HandleFunc("GET /api/compute/kernel", h.handleInstallKernel)
}

// The kernel's checksum is computed once per file version.
var kernelSum struct {
	sync.Mutex
	path, sum string
	size      int64
	mod       int64
}

// kernelFile returns this host's kernel binary and its SHA-256: what a new
// compute installs. It is the very binary this Compa runs Compitas with.
func kernelFile() (path, sum string, err error) {
	path = utils.FindKernelBinary()
	fi, err := os.Stat(path)
	if err != nil {
		return "", "", err
	}
	kernelSum.Lock()
	defer kernelSum.Unlock()
	if kernelSum.path == path && kernelSum.size == fi.Size() && kernelSum.mod == fi.ModTime().UnixNano() {
		return path, kernelSum.sum, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", "", err
	}
	defer f.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, f); err != nil {
		return "", "", err
	}
	kernelSum.path, kernelSum.sum, kernelSum.size, kernelSum.mod = path, hex.EncodeToString(hash.Sum(nil)), fi.Size(), fi.ModTime().UnixNano()
	return path, kernelSum.sum, nil
}

// handleInstallScript serves the script a new machine runs to become a
// compute. The one-time enrollment token is the credential; nothing here uses
// it up, enrolling does.
func (h *Handler) handleInstallScript(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if !h.computeStore().ValidEnrollment(token) {
		http.Error(w, "this install link is not valid, or has expired; add the compute again in Compa", http.StatusForbidden)
		return
	}
	_, sum, err := kernelFile()
	if err != nil {
		http.Error(w, "this Compa has no kernel binary to hand out; install compa-kernel on the machine yourself and run `compa-kernel compute enroll`", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, compute.InstallScript(requestBase(r), token, sum))
}

// handleInstallKernel serves the host's kernel to a machine with the same
// operating system and architecture.
func (h *Handler) handleInstallKernel(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !h.computeStore().ValidEnrollment(q.Get("token")) {
		http.Error(w, "invalid or expired token", http.StatusForbidden)
		return
	}
	if q.Get("os") != runtime.GOOS || q.Get("arch") != runtime.GOARCH {
		http.Error(w, "this Compa runs on "+runtime.GOOS+"/"+runtime.GOARCH+" and can only hand its kernel to the same; "+
			"install compa-kernel for "+q.Get("os")+"/"+q.Get("arch")+" yourself and run `compa-kernel compute enroll`", http.StatusNotFound)
		return
	}
	path, _, err := kernelFile()
	if err != nil {
		http.Error(w, "no kernel binary to hand out", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Cache-Control", "no-store")
	http.ServeFile(w, r, path)
}
