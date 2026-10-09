package api

import (
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

func computeMux(t *testing.T) *http.ServeMux {
	t.Helper()
	t.Setenv(config.EnvHome, t.TempDir())
	mux := http.NewServeMux()
	(&Handler{}).registerComputeRoutes(mux)
	return mux
}

func computeCall(mux *http.ServeMux, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Host = "compa.test:18800"
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	return rec
}

func TestComputeEnrollFlow(t *testing.T) {
	mux := computeMux(t)

	if rec := computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("add while disabled = %d, want 400", rec.Code)
	}
	if rec := computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`); rec.Code != http.StatusOK {
		t.Fatalf("enable = %d: %s", rec.Code, rec.Body)
	}

	rec := computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("add = %d: %s", rec.Code, rec.Body)
	}
	var added struct {
		Token         string `json:"token"`
		EnrollCommand string `json:"enroll_command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	if added.Token == "" || !strings.Contains(added.EnrollCommand, "http://compa.test:18800") {
		t.Fatalf("unexpected add response: %s", rec.Body)
	}

	body := `{"token":"` + added.Token + `","capabilities":{"docker":true}}`
	if rec := computeCall(mux, "POST", "/api/compute/enroll", body); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"credential"`) {
		t.Fatalf("enroll = %d: %s", rec.Code, rec.Body)
	}
	if rec := computeCall(mux, "POST", "/api/compute/enroll", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("token reuse = %d, want 401", rec.Code)
	}

	state := computeCall(mux, "GET", "/api/compute", "").Body.String()
	if strings.Contains(state, "credential") || strings.Contains(state, "hash") {
		t.Fatalf("state leaks secrets: %s", state)
	}
}

func TestComputeEnrollClientRoundTrip(t *testing.T) {
	mux := computeMux(t)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	rec := computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`)
	var added struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)

	id, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{})
	if err != nil {
		t.Fatalf("Enroll: %v", err)
	}
	if !compute.NewStore(config.GetHome()).Authenticate(id.ComputeID, id.Credential) {
		t.Fatal("issued credential does not authenticate")
	}
	if _, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{}); err == nil {
		t.Fatal("token reuse must fail")
	}

	home := t.TempDir()
	if err := compute.SaveIdentity(home, id); err != nil {
		t.Fatal(err)
	}
	if got, err := compute.LoadIdentity(home); err != nil || got != id {
		t.Fatalf("identity round trip: %v %+v", err, got)
	}
}

func fakeKernelBin(t *testing.T) string {
	t.Helper()
	skipWithoutShell(t)
	p := filepath.Join(t.TempDir(), "kernel")
	if err := os.WriteFile(p, []byte("#!/bin/sh\necho \"echo: $5\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCompitaChatLocalAndRemote(t *testing.T) {
	mux := computeMux(t)
	kernel := fakeKernelBin(t)
	t.Setenv(config.EnvBinary, kernel)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared"}`); rec.Code != http.StatusCreated {
		t.Fatalf("add local compita = %d: %s", rec.Code, rec.Body)
	}
	rec := computeCall(mux, "POST", "/api/compitas/ana/chat", `{"message":"hello"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "echo: hello") {
		t.Fatalf("local chat = %d: %s", rec.Code, rec.Body)
	}
	hist := computeCall(mux, "GET", "/api/compitas/ana/chat", "").Body.String()
	if strings.Count(hist, `"role"`) != 2 {
		t.Fatalf("history should hold both turns: %s", hist)
	}

	// Remote, dial-out: enroll, run the worker, chat through the broker.
	var added struct {
		Compute struct{ ID string } `json:"compute"`
		Token   string              `json:"token"`
	}
	rec = computeCall(mux, "POST", "/api/computes", `{"name":"box","mode":"dial_out"}`)
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	id, err := compute.Enroll(context.Background(), nil, srv.URL, added.Token, compute.Capabilities{})
	if err != nil {
		t.Fatal(err)
	}
	computeCall(mux, "POST", "/api/compitas", `{"name":"Bo","compute_id":"`+id.ComputeID+`","isolation":"shared"}`)

	if rec := computeCall(mux, "POST", "/api/compitas/bo/chat", `{"message":"hi"}`); !strings.Contains(rec.Body.String(), "offline") {
		t.Fatalf("offline compute should say so: %s", rec.Body)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go compute.Serve(ctx, id, compute.Runner{Home: t.TempDir(), Kernel: kernel}, func(string, ...any) {})
	deadline := time.Now().Add(5 * time.Second)
	for !computeBroker.Online(id.ComputeID) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	rec = computeCall(mux, "POST", "/api/compitas/bo/chat", `{"message":"remote"}`)
	if !strings.Contains(rec.Body.String(), "echo: remote") || strings.Contains(rec.Body.String(), `"error":true`) {
		t.Fatalf("remote chat = %s", rec.Body)
	}
}

func TestCompitaDialInAndPeerMessage(t *testing.T) {
	mux := computeMux(t)
	kernel := fakeKernelBin(t)
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)

	// A dial-in compute: Compa calls the machine's server.
	var secret string
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compute.DialInHandler(secret, compute.Runner{Home: t.TempDir(), Kernel: kernel}).ServeHTTP(w, r)
	}))
	defer remote.Close()
	rec := computeCall(mux, "POST", "/api/computes", `{"name":"lan","mode":"dial_in","url":"`+remote.URL+`"}`)
	var added struct {
		Token         string `json:"token"`
		EnrollCommand string `json:"enroll_command"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &added)
	secret = added.Token
	if rec.Code != http.StatusCreated || !strings.Contains(added.EnrollCommand, "compute serve --listen") {
		t.Fatalf("add dial-in = %d: %s", rec.Code, rec.Body)
	}
	if strings.Contains(computeCall(mux, "GET", "/api/compute", "").Body.String(), secret) {
		t.Fatal("state leaks the dial secret")
	}
	computeCall(mux, "POST", "/api/computes/lan/refresh", "")
	computeCall(mux, "POST", "/api/compitas", `{"name":"Cy","compute_id":"lan","isolation":"shared"}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared"}`)
	rec = computeCall(mux, "POST", "/api/compitas/cy/chat", `{"message":"over lan"}`)
	if !strings.Contains(rec.Body.String(), "echo: over lan") || strings.Contains(rec.Body.String(), `"error":true`) {
		t.Fatalf("dial-in chat = %s", rec.Body)
	}

	// Peer messages come from turns that are running.
	turnBases.Store("ana", "http://compa.test:18800")
	defer turnBases.Delete("ana")

	// Peer message: Ana (local) asks Cy (dial-in).
	peer := func(cred, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/compute/peer", strings.NewReader(body))
		req.Header.Set("X-Compute-ID", compute.LocalID)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := peer("wrong", `{"from":"ana","to":"Cy","message":"x"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad credential = %d", rec.Code)
	}
	rec = peer(localCredential("ana"), `{"from":"ana","to":"Cy","message":"lunch?"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "colleague Ana") {
		t.Fatalf("peer = %d: %s", rec.Code, rec.Body)
	}
	if rec := peer(localCredential("ana"), `{"from":"ana","to":"Cy","message":"x","depth":3}`); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("depth limit = %d", rec.Code)
	}
	if rec := peer(localCredential("cy"), `{"from":"cy","to":"Ana","message":"x"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a local credential must not speak for a remote Compita: %d", rec.Code)
	}
	if rec := peer(localCredential("ana"), `{"from":"ana","to":"Nobody","message":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown peer = %d", rec.Code)
	}
	// The dial-in Compita speaks as itself with its own secret, not as local,
	// from a turn of its own.
	turnBases.Store("cy", "http://compa.test:18800")
	defer turnBases.Delete("cy")
	back := httptest.NewRequest("POST", "/api/compute/peer", strings.NewReader(`{"from":"cy","to":"Ana","message":"hi"}`))
	back.Header.Set("X-Compute-ID", "lan")
	back.Header.Set("Authorization", "Bearer "+secret)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, back)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "colleague Cy") {
		t.Fatalf("dial-in peer = %d: %s", rec.Code, rec.Body)
	}
}

func TestCompitaObjectiveRunsToTheEnd(t *testing.T) {
	skipWithoutShell(t)
	objectivePause = time.Millisecond
	mux := computeMux(t)
	counter := filepath.Join(t.TempDir(), "n")
	kernel := filepath.Join(t.TempDir(), "kernel")
	script := "#!/bin/sh\necho x >> " + counter + "\nn=$(wc -l < " + counter + " | tr -d ' ')\n" +
		"if [ \"$n\" -lt 4 ]; then echo \"working, step $n\"; echo \"CONTINUE: step $((n+1))\"; else echo \"OBJECTIVE_DONE: all $n steps\"; fi\n"
	if err := os.WriteFile(kernel, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared"}`)

	if rec := computeCall(mux, "POST", "/api/compitas/ana/objective", `{"goal":""}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty goal = %d", rec.Code)
	}
	if rec := computeCall(mux, "POST", "/api/compitas/ana/objective", `{"goal":"ship the thing"}`); rec.Code != http.StatusCreated {
		t.Fatalf("set objective = %d: %s", rec.Code, rec.Body)
	}
	var v objectiveView
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		v = objectiveView{}
		_ = json.Unmarshal(computeCall(mux, "GET", "/api/compitas/ana/objective", "").Body.Bytes(), &v)
		if v.Objective != nil && v.Objective.Status != compute.ObjActive {
			break
		}
	}
	if v.Objective == nil || v.Objective.Status != compute.ObjDone || v.Objective.Turns != 5 || v.Objective.Summary != "all 5 steps" {
		t.Fatalf("objective did not run to the end: %+v", v.Objective)
	}
	if strings.Contains(computeCall(mux, "GET", "/api/compitas/ana/objective", "").Body.String(), "base_url") {
		t.Fatal("internal base_url leaked")
	}
	hist := computeCall(mux, "GET", "/api/compitas/ana/chat", "").Body.String()
	if !strings.Contains(hist, `"objective"`) || strings.Count(hist, "working, step") != 3 {
		t.Fatalf("chat should show the objective and each step: %s", hist)
	}
	if rec := computeCall(mux, "POST", "/api/compitas/ana/objective/pause", ""); rec.Code != http.StatusConflict {
		t.Fatalf("pausing an ended objective = %d", rec.Code)
	}
}

func TestCompitaObjectivePauseResumeStopAndRestart(t *testing.T) {
	skipWithoutShell(t)
	objectivePause = 5 * time.Millisecond
	t.Setenv(config.EnvHome, t.TempDir())
	h := &Handler{}
	mux := http.NewServeMux()
	h.registerComputeRoutes(mux)
	kernel := filepath.Join(t.TempDir(), "kernel")
	// $$ is this run's process id: a reply that differs every turn, on any shell
	// (the BSD date of macOS has no %N), so the repeated-reply guard stays out of it.
	if err := os.WriteFile(kernel, []byte("#!/bin/sh\nsleep 0.05\necho \"CONTINUE: more $$\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv(config.EnvBinary, kernel)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared"}`)
	view := func() objectiveView {
		var v objectiveView
		_ = json.Unmarshal(computeCall(mux, "GET", "/api/compitas/ana/objective", "").Body.Bytes(), &v)
		return v
	}
	waitTurns := func(more int) {
		start := view().Objective.Turns
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if view().Objective.Turns >= start+more {
				return
			}
		}
		t.Fatalf("no progress: %+v", view().Objective)
	}

	computeCall(mux, "POST", "/api/compitas/ana/objective", `{"goal":"keep going"}`)
	if rec := computeCall(mux, "POST", "/api/compitas/ana/objective", `{"goal":"second"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("a second active objective = %d", rec.Code)
	}
	waitTurns(2)

	computeCall(mux, "POST", "/api/compitas/ana/objective/pause", "")
	v := view()
	if v.Objective.Status != compute.ObjPaused || v.Running {
		t.Fatalf("after pause: %+v running=%v", v.Objective, v.Running)
	}
	turns := v.Objective.Turns
	time.Sleep(150 * time.Millisecond)
	if view().Objective.Turns != turns {
		t.Fatal("a paused objective kept working")
	}

	computeCall(mux, "POST", "/api/compitas/ana/objective/resume", "")
	waitTurns(2)

	// A launcher shutdown leaves it active; the next start picks it up.
	stopObjectiveRuns()
	time.Sleep(150 * time.Millisecond)
	if view().Objective.Status != compute.ObjActive || view().Running {
		t.Fatalf("after shutdown: %+v", view())
	}
	objectiveRuns.ctx, objectiveRuns.stop = context.WithCancel(context.Background())
	h.ResumeObjectives()
	waitTurns(2)

	computeCall(mux, "POST", "/api/compitas/ana/objective/stop", "")
	if view().Objective.Status != compute.ObjStopped {
		t.Fatalf("after stop: %+v", view().Objective)
	}
	if rec := computeCall(mux, "POST", "/api/compitas/ana/objective", `{"goal":"next"}`); rec.Code != http.StatusCreated {
		t.Fatalf("a new objective after stop = %d", rec.Code)
	}
	computeCall(mux, "POST", "/api/compitas/ana/objective/stop", "")
}

func TestCompitaApprovalsWaitInPlaceForTheOwner(t *testing.T) {
	mux := computeMux(t)
	computeCall(mux, "PUT", "/api/compute/enabled", `{"enabled":true}`)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Ana","compute_id":"this-computer","isolation":"shared","approvals":"open"}`)
	if rec := computeCall(mux, "POST", "/api/compitas", `{"name":"Zed","compute_id":"this-computer","isolation":"shared","approvals":"reckless"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown preset = %d", rec.Code)
	}
	var added struct {
		Token string `json:"token"`
	}
	_ = json.Unmarshal(computeCall(mux, "POST", "/api/computes", `{"name":"lan","mode":"dial_in","url":"http://127.0.0.1:9"}`).Body.Bytes(), &added)
	computeCall(mux, "POST", "/api/compitas", `{"name":"Cy","compute_id":"lan","isolation":"shared"}`)
	if !strings.Contains(computeCall(mux, "GET", "/api/compute", "").Body.String(), `"approvals":"open"`) {
		t.Fatal("the preset must be stored")
	}

	ask := func(cid, cred, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/compute/approval", strings.NewReader(body))
		req.Header.Set("X-Compute-ID", cid)
		req.Header.Set("Authorization", "Bearer "+cred)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}
	if rec := ask(compute.LocalID, "wrong", `{"from":"ana","tool":"exec"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad credential = %d", rec.Code)
	}
	if rec := ask(compute.LocalID, localCredential("cy"), `{"from":"cy","tool":"exec"}`); rec.Code != http.StatusForbidden {
		t.Fatalf("a compute must not ask for another compute's Compita: %d", rec.Code)
	}
	if rec := ask(compute.LocalID, localCredential("ana"), `{"from":"ana"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("no tool = %d", rec.Code)
	}
	// Compitas on one computer cannot speak for each other.
	computeCall(mux, "POST", "/api/compitas", `{"name":"Sam","compute_id":"this-computer","isolation":"shared"}`)
	if rec := ask(compute.LocalID, localCredential("ana"), `{"from":"sam","tool":"exec"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("Ana's credential must not ask as Sam: %d", rec.Code)
	}
	if rec := ask(compute.LocalID, localPeerSecret, `{"from":"ana","tool":"exec"}`); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the root secret is not a credential: %d", rec.Code)
	}

	for _, c := range []struct {
		cid, cred, from string
		approve         bool
		want            string
	}{
		{compute.LocalID, localCredential("ana"), "ana", true, `"approved":true`},
		{"lan", added.Token, "cy", false, `"approved":false`},
	} {
		got := make(chan *httptest.ResponseRecorder, 1)
		go func() {
			got <- ask(c.cid, c.cred, `{"from":"`+c.from+`","tool":"exec","arguments":{"command":"rm -rf build"}}`)
		}()
		var id string
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline) && id == ""; time.Sleep(10 * time.Millisecond) {
			var list struct {
				Approvals []compute.Approval `json:"approvals"`
			}
			_ = json.Unmarshal(computeCall(mux, "GET", "/api/compute/approvals?compita="+c.from, "").Body.Bytes(), &list)
			if len(list.Approvals) == 1 {
				id = list.Approvals[0].ID
				if list.Approvals[0].Summary != "exec: rm -rf build" {
					t.Fatalf("summary = %q", list.Approvals[0].Summary)
				}
			}
		}
		if id == "" {
			t.Fatal("the request never reached the owner")
		}
		view := objectiveView{}
		_ = json.Unmarshal(computeCall(mux, "GET", "/api/compitas/"+c.from+"/objective", "").Body.Bytes(), &view)
		if view.AwaitingApproval != 1 {
			t.Fatalf("the Compita should show as awaiting approval: %+v", view)
		}
		select {
		case <-got:
			t.Fatal("the turn must stay parked until the owner answers")
		case <-time.After(50 * time.Millisecond):
		}
		body := `{"approved":false}`
		if c.approve {
			body = `{"approved":true}`
		}
		if rec := computeCall(mux, "POST", "/api/compute/approvals/"+id, body); rec.Code != http.StatusOK {
			t.Fatalf("decide = %d", rec.Code)
		}
		if rec := <-got; rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), c.want) {
			t.Fatalf("answer = %d %s", rec.Code, rec.Body)
		}
		hist := computeCall(mux, "GET", "/api/compitas/"+c.from+"/chat", "").Body.String()
		if strings.Count(hist, `"approval"`) != 2 || !strings.Contains(hist, "Waiting for your approval: exec: rm -rf build") {
			t.Fatalf("the chat should keep the audit trail: %s", hist)
		}
	}
	if rec := computeCall(mux, "POST", "/api/compute/approvals/nope", `{"approved":true}`); rec.Code != http.StatusNotFound {
		t.Fatalf("answering nothing = %d", rec.Code)
	}
}
