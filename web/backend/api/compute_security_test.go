package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kayiik/compa/pkg/compute"
	"github.com/kayiik/compa/pkg/config"
)

// enrollDialOut adds a dial-out compute and enrolls it as its machine would,
// and returns the credential it was given.
func enrollDialOut(t *testing.T, mux *http.ServeMux, name string) string {
	t.Helper()
	rec := computeCall(mux, "POST", "/api/computes", `{"name":"`+name+`","mode":"dial_out"}`)
	var added struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	rec = computeCall(mux, "POST", "/api/compute/enroll", `{"token":"`+added.Token+`","capabilities":{}}`)
	var enrolled struct {
		Credential string `json:"credential"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &enrolled)
	if enrolled.Credential == "" {
		t.Fatalf("enroll %s = %d: %s", name, rec.Code, rec.Body)
	}
	return enrolled.Credential
}

// Any enrolled compute can post results. One must not be able to answer the
// turn of another's Compita: it would set what that Compita says, or whether
// its objective is done.
func TestAComputeCannotAnswerAnotherComputesTurnThroughTheAPI(t *testing.T) {
	mux := computeMux(t)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	victim := enrollDialOut(t, mux, "victim")
	evil := enrollDialOut(t, mux, "evil")

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	computeBroker.Next(ctx, "victim", time.Millisecond)
	got := make(chan compute.Result, 1)
	go func() {
		res, _ := computeBroker.Submit(ctx, "victim", compute.Job{CompitaID: "ana"})
		got <- res
	}()
	job, ok := computeBroker.Next(ctx, "victim", 5*time.Second)
	if !ok {
		t.Fatal("the victim's job never arrived")
	}

	post := func(id, cred string) {
		body, _ := json.Marshal(compute.Result{JobID: job.ID, Reply: "from " + id})
		req := httptest.NewRequest("POST", "/api/compute/result", bytes.NewReader(body))
		req.Header.Set("X-Compute-ID", id)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("result from %s = %d", id, rec.Code)
		}
	}
	post("evil", evil)
	select {
	case res := <-got:
		t.Fatalf("the turn was answered by a compute that does not run it: %q", res.Reply)
	case <-time.After(100 * time.Millisecond):
	}
	post("victim", victim)
	select {
	case res := <-got:
		if res.Reply != "from victim" {
			t.Fatalf("reply = %q", res.Reply)
		}
	case <-ctx.Done():
		t.Fatal("the real answer was never delivered")
	}
}

func peerRequest(host, from, cred, body string) *http.Request {
	req := httptest.NewRequest("POST", "/api/compute/peer", strings.NewReader(body))
	req.Host = host
	req.Header.Set("X-Compute-ID", compute.LocalID)
	req.Header.Set("Authorization", "Bearer "+cred)
	return req
}

func twoLocalCompitas(t *testing.T, script string) *http.ServeMux {
	t.Helper()
	skipWithoutShell(t)
	mux := computeMux(t)
	kernel := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(kernel, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	for _, name := range []string{"Ana", "Bo"} {
		if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"`+name+`","compute_id":"this-computer","isolation":"shared"}`); rec.Code != http.StatusCreated {
			t.Fatalf("add %s = %d: %s", name, rec.Code, rec.Body)
		}
	}
	return mux
}

// The colleague's turn gets the credential that is its own, and the address to
// send it to. A Compita can set any Host header on its request, so that
// address must not come from it: it would send the colleague's credential to
// wherever the Compita said.
func TestAPeerMessageReachesTheColleagueWhereTheSendersTurnDoes(t *testing.T) {
	mux := twoLocalCompitas(t, "#!/bin/sh\necho \"peer=$COMPA_PEER_URL\"\n")
	turnBases.Store("ana", "http://compa.test:18800")
	defer turnBases.Delete("ana")

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, peerRequest("attacker.example:9999", "ana", localCredential("ana"), `{"from":"ana","to":"Bo","message":"hi"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("peer = %d: %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "peer=http://compa.test:18800") || strings.Contains(rec.Body.String(), "attacker") {
		t.Fatalf("the colleague's turn must reach Compa where the sender's turn does, whatever Host the request names: %s", rec.Body)
	}
}

func TestAPeerMessageNeedsTheSendersTurnToBeRunning(t *testing.T) {
	mux := twoLocalCompitas(t, "#!/bin/sh\necho ran\n")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, peerRequest("compa.test:18800", "ana", localCredential("ana"), `{"from":"ana","to":"Bo","message":"hi"}`))
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "ran") {
		t.Fatalf("a message from a Compita with no turn running = %d: %s", rec.Code, rec.Body)
	}
}
