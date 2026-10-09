package compute

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// linkOrSkip makes a symlink, or skips the test where one cannot be made
// (Windows needs a privilege for it).
func linkOrSkip(t *testing.T, target, name string) {
	t.Helper()
	if err := os.Symlink(target, name); err != nil {
		t.Skipf("cannot make a symlink here: %v", err)
	}
}

func put(t *testing.T, name, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func slurp(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// A Compita can write its folder, and a container mounts it. A link it leaves
// there to a file elsewhere must never make Compa write that file: the
// machine's keys would land in it, or it would be overwritten.
func TestPrepareNeverWritesThroughALinkTheCompitaLeft(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	machine := map[string]string{
		"config.json": hostConfig, "auth.json": `{"key":"REAL-KEY"}`,
		"model_catalogs.json": `{}`, ".security.yml": "web:\n  tavily:\n    api_key: K\n",
	}
	for name, body := range machine {
		put(t, filepath.Join(home, name), body)
	}
	r := Runner{Home: home}
	dir := r.CompitaHome("ana")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victims := map[string]string{}
	for name := range machine {
		victim := filepath.Join(outside, name)
		put(t, victim, "untouched")
		linkOrSkip(t, victim, filepath.Join(dir, name))
		victims[name] = victim
	}

	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationContainer}); err != nil {
		t.Fatal(err)
	}
	for name, victim := range victims {
		if got := slurp(t, victim); got != "untouched" {
			t.Errorf("%s was written through the Compita's link: %q", name, got)
		}
		fi, err := os.Lstat(filepath.Join(dir, name))
		if err != nil || !fi.Mode().IsRegular() {
			t.Errorf("%s must be a file of the Compita's own after a turn: %v, %v", name, fi, err)
		}
	}
	if got := slurp(t, filepath.Join(dir, "auth.json")); got != machine["auth.json"] {
		t.Errorf("the Compita still gets the machine's keys: %q", got)
	}
}

// The relative link that climbs out of the folder is the same escape.
func TestPrepareRefusesALinkThatClimbsOutOfTheFolder(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, "auth.json"), `{"key":"REAL-KEY"}`)
	r := Runner{Home: home}
	dir := r.CompitaHome("ana")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(home, "victim")
	put(t, victim, "untouched")
	// Older than the machine's file, which is when a seed is copied.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(victim, past, past); err != nil {
		t.Fatal(err)
	}
	linkOrSkip(t, filepath.Join("..", "..", "victim"), filepath.Join(dir, "auth.json"))

	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationContainer}); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, victim); got != "untouched" {
		t.Fatalf("a relative link out of the folder was followed: %q", got)
	}
}

// A browser Compita's profile is in the folder too: a link in its place must
// not make Compa clear or rewrite files elsewhere.
func TestBrowserPrepareStaysInsideTheFolder(t *testing.T) {
	home, outside := t.TempDir(), t.TempDir()
	r := Runner{Home: home}
	dir := r.CompitaHome("ana")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	crashed := `{"profile":{"exit_type":"Crashed"}}`
	put(t, filepath.Join(outside, "Default", "Preferences"), crashed)
	put(t, filepath.Join(outside, "SingletonLock"), "keep")
	linkOrSkip(t, outside, filepath.Join(dir, "browser-profile"))

	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationContainer, Browser: true}); err != nil {
		t.Fatal(err)
	}
	if got := slurp(t, filepath.Join(outside, "Default", "Preferences")); got != crashed {
		t.Errorf("a file outside the folder was rewritten: %q", got)
	}
	if got := slurp(t, filepath.Join(outside, "SingletonLock")); got != "keep" {
		t.Errorf("a file outside the folder was removed: %q", got)
	}
}

// What Compa knows about a Compita, its budgets, the address it reaches Compa
// at and a webhook's token, must not be where the Compita can edit it.
func TestWhatCompaKnowsAboutACompitaIsKeptOutOfItsFolder(t *testing.T) {
	home := t.TempDir()
	dir := Runner{Home: home}.CompitaHome("ana")

	if err := AppendChat(home, "ana", ChatMessage{Role: "user", Text: "hi", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := StartObjective(home, "ana", "ship it", "http://compa.test", 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := AddSchedule(home, "ana", "check the inbox", 5, "http://compa.test", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); err == nil {
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			t.Errorf("%s is in the Compita's own folder", e.Name())
		}
	}
	for _, name := range []string{"chat.jsonl", "objective.json", "triggers.json"} {
		if _, err := os.Stat(filepath.Join(home, "compita-state", "ana", name)); err != nil {
			t.Errorf("%s must be kept by Compa: %v", name, err)
		}
	}
}

func TestACompitaIdIsNeverAPath(t *testing.T) {
	home := t.TempDir()
	for _, id := range []string{"", ".", "..", "../x", "a/b", `a\b`, ".hidden", "Ana", "x y", strings.Repeat("a", 200)} {
		if err := AppendChat(home, id, ChatMessage{Role: "user", Text: "x"}); err == nil {
			t.Errorf("chat accepted %q", id)
		}
		if _, err := LoadChat(home, id, 10); err == nil {
			t.Errorf("chat load accepted %q", id)
		}
		if _, err := StartObjective(home, id, "g", "", 0, 0); err == nil {
			t.Errorf("objective accepted %q", id)
		}
		if _, err := LoadTriggers(home, id); err == nil {
			t.Errorf("triggers accepted %q", id)
		}
		if _, err := (Runner{Home: home}).prepare(Turn{CompitaID: id}); err == nil {
			t.Errorf("a turn accepted %q", id)
		}
	}
	if entries, _ := os.ReadDir(home); len(entries) != 0 {
		t.Errorf("a bad id left %d entries in the home", len(entries))
	}
	for _, id := range []string{"ana", "ana-2", "a1", "item"} {
		if !validID(id) {
			t.Errorf("%q is an id Compa makes", id)
		}
	}
}

const machineSecrets = `channel_list:
  slack:
    settings:
      bot_token: xoxb-CHANNEL-SECRET
web:
  tavily:
    api_key: TAVILY-KEY
skills:
  registries:
    github:
      auth_token: SKILLS-KEY
mcp:
  servers:
    github:
      env:
        TOKEN: MCP-SECRET
hooks:
  processes:
    audit:
      env:
        TOKEN: HOOK-SECRET
provider_instances:
  main:
    api_key: PROVIDER-KEY
`

// A Compita is given the machine's model setup, not its chat apps, MCP servers
// or hooks: the secrets of those must not reach it either.
func TestACompitaGetsNoSecretsOfTheMachinesChatAppsConnectionsOrHooks(t *testing.T) {
	out, err := buildCompitaSecurity([]byte(machineSecrets))
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"CHANNEL-SECRET", "MCP-SECRET", "HOOK-SECRET", "channel_list", "mcp:", "hooks:"} {
		if strings.Contains(string(out), gone) {
			t.Errorf("%q must not reach a Compita:\n%s", gone, out)
		}
	}
	for _, kept := range []string{"TAVILY-KEY", "SKILLS-KEY", "PROVIDER-KEY"} {
		if !strings.Contains(string(out), kept) {
			t.Errorf("%q is a model or tool key the Compita needs:\n%s", kept, out)
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal(out, &doc); err != nil || len(doc) != 3 {
		t.Errorf("what is left must still be a .security.yml: %v, %v", doc, err)
	}

	if out, err := buildCompitaSecurity(nil); err != nil || len(out) != 0 {
		t.Errorf("an empty file stays empty: %q, %v", out, err)
	}
	if _, err := buildCompitaSecurity([]byte("a: [")); err == nil {
		t.Error("a file that cannot be read must not be copied as it is")
	}
	if _, err := buildCompitaSecurity([]byte("- a\n- b\n")); err == nil {
		t.Error("a file that is not a mapping must not be copied as it is")
	}
}

func TestPrepareSeedsTheFilteredSecretsAndReplacesAnOldFullCopy(t *testing.T) {
	home := t.TempDir()
	put(t, filepath.Join(home, ".security.yml"), machineSecrets)
	r := Runner{Home: home}
	dir := r.CompitaHome("ana")
	// An earlier copy, made before the filter, and newer than the machine's.
	put(t, filepath.Join(dir, ".security.yml"), machineSecrets)
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(dir, ".security.yml"), later, later); err != nil {
		t.Fatal(err)
	}

	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared}); err != nil {
		t.Fatal(err)
	}
	got := slurp(t, filepath.Join(dir, ".security.yml"))
	if strings.Contains(got, "CHANNEL-SECRET") || strings.Contains(got, "MCP-SECRET") || !strings.Contains(got, "PROVIDER-KEY") {
		t.Fatalf("a Compita's .security.yml = %s", got)
	}
	st1, _ := os.Stat(filepath.Join(dir, ".security.yml"))
	if _, err := r.prepare(Turn{CompitaID: "ana", Isolation: IsolationShared}); err != nil {
		t.Fatal(err)
	}
	if st2, _ := os.Stat(filepath.Join(dir, ".security.yml")); !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("an unchanged .security.yml must not be rewritten every turn")
	}
}

// A compute may answer only for the jobs it was given. Without that, any
// enrolled compute could answer another's turn, and set what its Compita says
// or whether its objective is done.
func TestAComputeCannotAnswerAnotherComputesJob(t *testing.T) {
	b := NewBroker()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, id := range []string{"victim", "evil"} {
		b.Next(ctx, id, time.Millisecond)
	}
	type outcome struct {
		res Result
		err error
	}
	got := make(chan outcome, 1)
	go func() {
		res, err := b.Submit(ctx, "victim", Job{CompitaID: "ana"})
		got <- outcome{res, err}
	}()
	job, ok := b.Next(ctx, "victim", 5*time.Second)
	if !ok {
		t.Fatal("the victim's job never arrived")
	}

	if b.Complete("evil", Result{JobID: job.ID, Reply: "FORGED"}) {
		t.Fatal("a compute answered a job it does not run")
	}
	select {
	case o := <-got:
		t.Fatalf("the waiting turn was answered by the wrong compute: %q, %v", o.res.Reply, o.err)
	case <-time.After(100 * time.Millisecond):
	}
	if !b.Complete("victim", Result{JobID: job.ID, Reply: "real"}) {
		t.Fatal("the compute that runs the job must be able to answer it")
	}
	select {
	case o := <-got:
		if o.err != nil || o.res.Reply != "real" {
			t.Fatalf("answer = %q, %v", o.res.Reply, o.err)
		}
	case <-ctx.Done():
		t.Fatal("the real answer was never delivered")
	}
}

func TestJobIdsCannotBeGuessed(t *testing.T) {
	b := NewBroker()
	ctx := context.Background()
	b.Next(ctx, "box", time.Millisecond)
	for range 3 {
		if err := b.Enqueue("box", Job{}); err != nil {
			t.Fatal(err)
		}
	}
	old := regexp.MustCompile(`^\d{14}-\d+$`)
	seen := map[string]bool{}
	for range 3 {
		job, ok := b.Next(ctx, "box", time.Second)
		if !ok {
			t.Fatal("a queued job did not come out")
		}
		if len(job.ID) < 20 || old.MatchString(job.ID) || seen[job.ID] {
			t.Fatalf("job id %q is guessable or repeated", job.ID)
		}
		seen[job.ID] = true
	}
}
