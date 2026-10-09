package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

func TestBrowserNeedsAContainerOrABrowserOnTheCompute(t *testing.T) {
	if compute.Detect().Browser {
		t.Skip("this machine has a browser of its own, so a refusal cannot be shown")
	}
	mux := computeMux(t)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Web","compute_id":"this-computer","isolation":"shared","browser":true}`); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "no browser") {
		t.Fatalf("a browser on a compute without one = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(computeCall(mux, "GET", "/api/compute", "").Body.String(), `"name":"Web"`) {
		t.Fatal("a refused Compita must not be created")
	}

	// A machine with a browser of its own (the browser image) gives one at any level.
	s := compute.NewStore(config.GetHome())
	s.Detect = func() compute.Capabilities { return compute.Capabilities{Browser: true} }
	if err := s.RefreshLocal(); err != nil {
		t.Fatal(err)
	}
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Web","compute_id":"this-computer","isolation":"shared","browser":true}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"browser":true`) {
		t.Fatalf("a browser on a compute that has one = %d %s", rec.Code, rec.Body)
	}
}

func TestBrowserInAContainer(t *testing.T) {
	if !compute.DetectDocker().Docker {
		t.Skip("no Docker here")
	}
	mux := computeMux(t)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Probe9137","compute_id":"this-computer","isolation":"container","browser":true}`); rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), `"browser":true`) {
		t.Fatalf("a browser in a container = %d %s", rec.Code, rec.Body)
	}
}
