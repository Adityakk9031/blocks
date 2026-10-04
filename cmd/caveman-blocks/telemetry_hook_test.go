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

// hookTelemetryFixture is a repo, isolated state, and a saved yes under that state.
func hookTelemetryFixture(t *testing.T) (root string, o telemetry.Options, calls *[][]telemetry.Event) {
	t.Helper()
	root = hookRepo(t)
	for _, k := range []string{"DO_NOT_TRACK", "CAVEMAN_BLOCKS_TELEMETRY", "CAVEMAN_TELEMETRY", "CI"} {
		t.Setenv(k, "")
	}
	o, _, calls = cliTelemetry(t)
	var err error
	if o.Base, err = repo.StateBase(); err != nil {
		t.Fatal(err)
	}
	telemetryOptions = func() telemetry.Options { return o }
	if _, err := telemetry.SaveChoice(o, true, true); err != nil {
		t.Fatal(err)
	}
	return root, o, calls
}

func TestHookTelemetryCadenceAndHarness(t *testing.T) {
	root, o, calls := hookTelemetryFixture(t)
	now := o.Now()
	invoke := func(harness, session string) {
		t.Helper()
		out, base := hookAnswer(harness, "", hookPayload(session, root, "caveman-blocks run json-peek"), o.Now)
		if string(out) != "{}\n" || base != o.Base {
			t.Fatalf("answer %s, base %q", out, base)
		}
		hookTelemetry(base, now)
	}
	invoke("codex", "s1")
	if len(*calls) != 1 || len((*calls)[0]) != 0 || telemetry.FlushDue(o.Base, now) {
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
	now = now.Add(25 * time.Hour)
	invoke("claude", "s3")
	if len(*calls) != 1 || telemetry.FlushDue(o.Base, now) {
		t.Fatal("off hook spawned or failed to touch marker")
	}
}

// Outside a repo and on a post-run call the hook does no telemetry work at all.
func TestHookTelemetryAbsentOutsideAndPost(t *testing.T) {
	root := hookRepo(t)
	if _, base := hookAnswer("claude", "", hookPayload("outside", t.TempDir(), "ls"), time.Now); base != "" {
		t.Fatalf("outside a repo: base %q", base)
	}
	post := strings.ReplaceAll(string(hookPayload("post", root, "ls")), "PreToolUse", "PostToolUse")
	if _, base := hookAnswer("claude", "post", []byte(post), time.Now); base != "" {
		t.Fatalf("post phase: base %q", base)
	}
	if _, base := hookAnswer("no-such-harness", "", hookPayload("x", root, "ls"), time.Now); base != "" {
		t.Fatalf("unknown harness: base %q", base)
	}
}

// A hook that runs with an off switch notes the day and starts nothing, and that day's usage is
// never sent once the switch is gone (internal/telemetry TestRollupSkipsOffDays).
func TestHookTelemetryNotesEnvOff(t *testing.T) {
	_, o, calls := hookTelemetryFixture(t)
	t.Setenv("DO_NOT_TRACK", "1")
	o.Getenv = os.Getenv
	telemetryOptions = func() telemetry.Options { return o }
	hookTelemetry(o.Base, o.Now())
	fi, err := os.Stat(filepath.Join(o.Base, "telemetry-off"))
	if err != nil || !fi.ModTime().Equal(o.Now()) {
		t.Fatalf("off day not noted: %v", err)
	}
	if len(*calls) != 0 {
		t.Fatal("flush started under DO_NOT_TRACK")
	}
}

// Nothing in the telemetry step may take the hook down: a non-zero exit denies the tool call.
func TestHookTelemetryNeverPanics(t *testing.T) {
	_, o, _ := hookTelemetryFixture(t)
	telemetryOptions = func() telemetry.Options { panic("telemetry state exploded") }
	hookTelemetry(o.Base, o.Now())
}
