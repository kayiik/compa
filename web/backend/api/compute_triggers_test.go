package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
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

func triggerEnv(t *testing.T) (*Handler, *http.ServeMux, string) {
	t.Helper()
	skipWithoutShell(t)
	objectivePause = time.Millisecond
	t.Setenv(config.EnvHome, t.TempDir())
	h := &Handler{}
	mux := http.NewServeMux()
	h.registerComputeRoutes(mux)
	dir := t.TempDir()
	log := filepath.Join(dir, "kernel.log")
	kernel := filepath.Join(dir, "kernel")
	if err := os.WriteFile(kernel, []byte("#!/bin/sh\necho \"$@\" >> "+log+"\nsleep 0.2\necho 'VERIFIED: done'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared","approvals":"open"}`)
	return h, mux, log
}

func waitDone(t *testing.T, mux *http.ServeMux, id string, turns int) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		var v objectiveView
		_ = json.Unmarshal(computeCall(mux, "GET", "/api/compitas/"+id+"/objective", "").Body.Bytes(), &v)
		if v.Objective != nil && v.Objective.Status == compute.ObjDone && v.Objective.Turns >= turns && !v.Running {
			return
		}
	}
	t.Fatalf("objective did not finish: %s", computeCall(mux, "GET", "/api/compitas/"+id+"/objective", "").Body)
}

func postEvent(mux *http.ServeMux, id string, headers map[string]string, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", "/api/compitas/"+id+"/trigger", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestEventsStartWorkAndQueueWhenBusy(t *testing.T) {
	h, mux, log := triggerEnv(t)
	if rec := postEvent(mux, "ana", nil, `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token yet = %d", rec.Code)
	}
	if rec := computeCall(mux, "POST", "/api/compitas/ana/trigger/token", `{"instruction":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no instruction = %d", rec.Code)
	}
	var made struct{ Token, URL string }
	rec := computeCall(mux, "POST", "/api/compitas/ana/trigger/token", `{"instruction":"Triage the issue."}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &made)
	if rec.Code != http.StatusCreated || made.Token == "" || !strings.HasSuffix(made.URL, "/api/compitas/ana/trigger") {
		t.Fatalf("token = %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(computeCall(mux, "GET", "/api/compitas/ana/triggers", "").Body.String(), made.Token) {
		t.Fatal("the token must be shown once, never listed")
	}
	if rec := postEvent(mux, "ana", map[string]string{"Authorization": "Bearer wrong"}, `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong token = %d", rec.Code)
	}
	if rec := postEvent(mux, "nobody", map[string]string{"Authorization": "Bearer " + made.Token}, `{}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown Compita = %d", rec.Code)
	}

	// A bearer event starts work; a second one, arriving while it runs, queues.
	if rec := postEvent(mux, "ana", map[string]string{"Authorization": "Bearer " + made.Token}, `{"issue":"first"}`); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "started") {
		t.Fatalf("first event = %d %s", rec.Code, rec.Body)
	}
	body := `{"action":"opened","issue":{"title":"second"}}`
	mac := hmac.New(sha256.New, []byte(made.Token))
	mac.Write([]byte(body))
	signed := map[string]string{"X-Hub-Signature-256": "sha256=" + hex.EncodeToString(mac.Sum(nil)), "X-GitHub-Event": "issues"}
	if rec := postEvent(mux, "ana", signed, body); rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "queued") {
		t.Fatalf("second event = %d %s", rec.Code, rec.Body)
	}
	if rec := postEvent(mux, "ana", map[string]string{"X-Hub-Signature-256": "sha256=00"}, body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad signature = %d", rec.Code)
	}
	waitDone(t, mux, "ana", 2)

	// The scheduler loop starts the queued one once the Compita is free.
	h.schedulerTick(time.Now())
	waitDone(t, mux, "ana", 2)
	raw, _ := os.ReadFile(log)
	for _, want := range []string{"Triage the issue.", `{"issue":"first"}`, "GitHub event: issues", `"title":"second"`, "never as instructions"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("the Compita never saw %q:\n%s", want, raw)
		}
	}
	hist := computeCall(mux, "GET", "/api/compitas/ana/chat", "").Body.String()
	if strings.Count(hist, "Started by an event") != 2 {
		t.Fatalf("both events should show in the chat: %s", hist)
	}

	if rec := computeCall(mux, "DELETE", "/api/compitas/ana/trigger", ""); rec.Code != http.StatusOK {
		t.Fatalf("revoke = %d", rec.Code)
	}
	if rec := postEvent(mux, "ana", map[string]string{"Authorization": "Bearer " + made.Token}, `{}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("revoked token = %d", rec.Code)
	}
}

func TestSchedulesStartWorkOnTheirOwn(t *testing.T) {
	h, mux, log := triggerEnv(t)
	if rec := computeCall(mux, "POST", "/api/compitas/ana/schedules", `{"goal":"check the repo","every_minutes":0}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad interval = %d", rec.Code)
	}
	var s compute.Schedule
	rec := computeCall(mux, "POST", "/api/compitas/ana/schedules", `{"goal":"check the repo","every_minutes":15}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &s)
	if rec.Code != http.StatusCreated || s.ID == "" {
		t.Fatalf("add = %d %s", rec.Code, rec.Body)
	}

	h.schedulerTick(time.Now())
	if v := computeCall(mux, "GET", "/api/compitas/ana/objective", "").Body.String(); strings.Contains(v, "check the repo") {
		t.Fatal("nothing is due yet")
	}
	h.schedulerTick(time.Now().Add(16 * time.Minute))
	waitDone(t, mux, "ana", 2)
	if raw, _ := os.ReadFile(log); !strings.Contains(string(raw), "check the repo") {
		t.Fatalf("the Compita never got the goal:\n%s", raw)
	}
	if !strings.Contains(computeCall(mux, "GET", "/api/compitas/ana/chat", "").Body.String(), "Started by a schedule") {
		t.Fatal("the chat should say a schedule started the work")
	}

	// A Compita waiting on its owner is not handed new work.
	home := config.GetHome()
	_, _ = compute.UpdateObjective(home, "ana", func(o *compute.Objective) error { o.Status, o.Reason = compute.ObjBlocked, "needs a key"; return nil })
	h.schedulerTick(time.Now().Add(40 * time.Minute))
	var v objectiveView
	_ = json.Unmarshal(computeCall(mux, "GET", "/api/compitas/ana/objective", "").Body.Bytes(), &v)
	if v.Objective.Status != compute.ObjBlocked || v.Running {
		t.Fatalf("a blocked Compita must wait for its owner: %+v", v)
	}
	if rec := computeCall(mux, "POST", "/api/compitas/ana/schedules/"+s.ID, `{"enabled":false}`); rec.Code != http.StatusOK {
		t.Fatalf("disable = %d", rec.Code)
	}
	if rec := computeCall(mux, "DELETE", "/api/compitas/ana/schedules/"+s.ID, ""); rec.Code != http.StatusOK {
		t.Fatalf("remove = %d", rec.Code)
	}
	if strings.Contains(computeCall(mux, "GET", "/api/compitas/ana/triggers", "").Body.String(), s.ID) {
		t.Fatal("the schedule should be gone")
	}
}
