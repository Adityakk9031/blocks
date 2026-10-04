package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/caveman-ai/blocks/internal/repo"
	"github.com/caveman-ai/blocks/internal/stats"
	"github.com/caveman-ai/blocks/internal/telemetry"
)

func TestHookTelemetryCadenceAndHarness(t *testing.T) {
	root := hookRepo(t)
	o, _, calls := cliTelemetry(t)
	var err error
	o.Base, err = repo.StateBase()
	if err != nil {
		t.Fatal(err)
	}
	telemetryOptions = func() telemetry.Options { return o }
	if _, err := telemetry.SaveChoice(o, true, true); err != nil {
		t.Fatal(err)
	}
	invoke := func(harness, session string) {
		t.Helper()
		out := hookAnswer(harness, "", hookPayload(session, root, "caveman-blocks run json-peek"), o.Now)
		hookBackground.Wait()
		if string(out) != "{}\n" {
			t.Fatalf("telemetry changed answer: %s", out)
		}
	}
	invoke("codex", "s1")
	if len(*calls) != 1 || len((*calls)[0]) != 0 || telemetry.FlushDue(o.Base, o.Now()) {
		t.Fatalf("first daily attempt: %+v", *calls)
	}
	// A warm marker does not even resolve telemetry options.
	telemetryOptions = func() telemetry.Options { t.Error("warm hook read telemetry state"); return o }
	invoke("claude", "s2")
	telemetryOptions = func() telemetry.Options { return o }
	if len(*calls) != 1 {
		t.Fatal("spawned twice in one day")
	}
	stateDir, err := repo.StateDir(root)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "stats.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		var event stats.Event
		if err := json.Unmarshal([]byte(line), &event); err != nil || event.Harness != []string{"codex", "claude"}[i] {
			t.Fatalf("harness: %+v %v", event, err)
		}
	}
	if _, err := telemetry.SaveChoice(o, false, true); err != nil {
		t.Fatal(err)
	}
	later := o.Now().Add(25 * time.Hour)
	o.Now = func() time.Time { return later }
	invoke("claude", "s3")
	if len(*calls) != 1 || telemetry.FlushDue(o.Base, later) {
		t.Fatal("off hook spawned or failed to touch marker")
	}
}

func TestHookTelemetryAbsentOutsideAndPost(t *testing.T) {
	root := hookRepo(t)
	o, _, calls := cliTelemetry(t)
	telemetryOptions = func() telemetry.Options { t.Error("excluded hook resolved telemetry"); return o }
	hookAnswer("claude", "", hookPayload("outside", t.TempDir(), "ls"), o.Now)
	post := strings.ReplaceAll(string(hookPayload("post", root, "ls")), "PreToolUse", "PostToolUse")
	hookAnswer("claude", "post", []byte(post), o.Now)
	hookBackground.Wait()
	base, err := repo.StateBase()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(base, "telemetry-attempt")); !os.IsNotExist(err) || len(*calls) != 0 {
		t.Fatal("outside/post hook did telemetry")
	}
}

func TestHookTelemetryWaitsForAnswer(t *testing.T) {
	root := hookRepo(t)
	o, _, calls := cliTelemetry(t)
	var err error
	o.Base, err = repo.StateBase()
	if err != nil {
		t.Fatal(err)
	}
	telemetryOptions = func() telemetry.Options { return o }
	if _, err := telemetry.SaveChoice(o, true, true); err != nil {
		t.Fatal(err)
	}
	answered := make(chan struct{})
	hookAnswer("claude", "", hookPayload("before-answer", root, "ls"), o.Now, answered)
	if _, err := os.Stat(filepath.Join(o.Base, "telemetry-attempt")); !os.IsNotExist(err) {
		close(answered)
		t.Fatal("telemetry touched its marker before the answer")
	}
	close(answered)
	hookBackground.Wait()
	if len(*calls) != 1 {
		t.Fatal("telemetry did not start after the answer")
	}
}
