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

// hookTelemetryFixture is a repo, isolated state, and a yes saved under that state. set swaps the
// options the hook sees, for a test that moves the clock or the environment.
func hookTelemetryFixture(t *testing.T, explicit bool) (root string, o telemetry.Options, calls *[][]telemetry.Event, set func(telemetry.Options)) {
	t.Helper()
	root = hookRepo(t)
	o, _, calls = cliTelemetry(t)
	var err error
	if o.Base, err = repo.StateBase(); err != nil {
		t.Fatal(err)
	}
	set = func(next telemetry.Options) { telemetryOptions = func() telemetry.Options { return next } }
	set(o)
	if _, err := telemetry.SaveChoice(o, true, explicit); err != nil {
		t.Fatal(err)
	}
	return root, o, calls, set
}

func marked(o telemetry.Options) bool {
	_, err := os.Stat(filepath.Join(o.Base, "telemetry-off"))
	return err == nil
}

func TestHookTelemetryCadenceAndHarness(t *testing.T) {
	root, o, calls, set := hookTelemetryFixture(t, true)
	invoke := func(harness, session string) (sendable bool) {
		t.Helper()
		out, sendable := hookAnswer(harness, "", hookPayload(session, root, "caveman-blocks run json-peek"), o.Now)
		if string(out) != "{}\n" {
			t.Fatalf("telemetry changed the answer: %s", out)
		}
		if sendable {
			hookFlush()
		}
		return sendable
	}
	if !invoke("codex", "s1") || len(*calls) != 1 || len((*calls)[0]) != 0 || telemetry.FlushDue(o.Base, o.Now()) {
		t.Fatalf("first daily attempt: %+v", *calls)
	}
	if !invoke("claude", "s2") || len(*calls) != 1 || marked(o) {
		t.Fatal("spawned twice in one day, or marked an on day off")
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
	set(o)
	// Off: the day is marked, nothing starts, and the daily attempt is not used up.
	if invoke("claude", "s3") || len(*calls) != 1 || !marked(o) || !telemetry.FlushDue(o.Base, later) {
		t.Fatal("off hook started a flush, left the day unmarked, or used up the daily attempt")
	}
}

// A hook that enrolls under the Caveman CLI's notice sends first_run once; later calls do not.
func TestHookEnrollSendsFirstRunOnce(t *testing.T) {
	root := hookRepo(t)
	o, _, calls := cliTelemetry(t)
	hub := `{"telemetry":{"enabled":true,"decidedAt":"2026-10-02T00:00:00Z","promptVersion":6,"anonymousId":"abcdef01-2345-4678-89ab-cdef01234567"}}`
	if err := os.MkdirAll(filepath.Join(o.Home, ".caveman"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.Home, ".caveman", "cloud.json"), []byte(hub), 0o644); err != nil {
		t.Fatal(err)
	}
	firstRuns := func() (n int) {
		for _, events := range *calls {
			for _, e := range events {
				if e.Event == "first_run" {
					n++
				}
			}
		}
		return n
	}
	for i := range 2 {
		if _, sendable := hookAnswer("claude", "", hookPayload("s", root, "ls"), o.Now); !sendable {
			t.Fatalf("call %d: not sendable", i)
		}
		if n := firstRuns(); n != 1 {
			t.Fatalf("call %d: %d first_run events: %+v", i, n, *calls)
		}
	}
	if e := (*calls)[0][0]; e.AnonymousID != "abcdef01-2345-4678-89ab-cdef01234567" || !e.Linked {
		t.Fatalf("first_run identity: %+v", e)
	}
}

// Outside a repo and on a post-run call the hook does no telemetry work at all.
func TestHookTelemetryAbsentOutsideAndPost(t *testing.T) {
	root, o, _, _ := hookTelemetryFixture(t, true)
	if _, sendable := hookAnswer("claude", "", hookPayload("in", root, "ls"), o.Now); !sendable {
		t.Fatal("pre-run call inside a repo with telemetry on is not sendable")
	}
	telemetryOptions = func() telemetry.Options { t.Error("excluded hook call read telemetry state"); return o }
	if _, sendable := hookAnswer("claude", "", hookPayload("outside", t.TempDir(), "ls"), o.Now); sendable {
		t.Fatal("outside a repo")
	}
	post := strings.ReplaceAll(string(hookPayload("post", root, "ls")), "PreToolUse", "PostToolUse")
	if _, sendable := hookAnswer("claude", "post", []byte(post), o.Now); sendable {
		t.Fatal("post phase")
	}
	if _, sendable := hookAnswer("no-such-harness", "", hookPayload("x", root, "ls"), o.Now); sendable {
		t.Fatal("unknown harness")
	}
}

// A hook call that finds telemetry off marks the day before it counts anything, whatever turned it
// off, so that day's counts are never sent once it is on again (internal/telemetry
// TestRollupSkipsOffDays). CI never hides a switch or a choice behind it.
func TestHookTelemetryMarksOffDays(t *testing.T) {
	env := func(vars map[string]string) func(string) string {
		return func(k string) string { return vars[k] }
	}
	t.Run("environment switch", func(t *testing.T) {
		root, o, calls, set := hookTelemetryFixture(t, true)
		o.Getenv = env(map[string]string{"DO_NOT_TRACK": "1"})
		set(o)
		_, sendable := hookAnswer("claude", "", hookPayload("s", root, "ls"), o.Now)
		fi, err := os.Stat(filepath.Join(o.Base, "telemetry-off"))
		if sendable || err != nil || !fi.ModTime().Equal(o.Now()) || len(*calls) != 0 {
			t.Fatalf("DO_NOT_TRACK: sendable %v, marker %v, started %+v", sendable, err, *calls)
		}
	})
	t.Run("the Caveman CLI opt-out, later removed", func(t *testing.T) {
		_, o, _, _ := hookTelemetryFixture(t, false)
		config := filepath.Join(o.Home, ".caveman-cloud", "config.json")
		if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte(`{"telemetry":{"enabled":false,"decidedAt":"2026-10-04T08:00:00Z","promptVersion":5}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if hookObserve() || !marked(o) {
			t.Fatal("opt-out not marked")
		}
		// The config disappears: telemetry is on again, but today stays marked.
		if err := os.Remove(config); err != nil {
			t.Fatal(err)
		}
		if d := telemetry.Decide(o); !d.Sendable || !marked(o) {
			t.Fatalf("after removal: %+v", d)
		}
	})
	t.Run("never enrolled", func(t *testing.T) {
		hookRepo(t)
		o, _, _ := cliTelemetry(t)
		var err error
		if o.Base, err = repo.StateBase(); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(o.Base, 0o700); err != nil {
			t.Fatal(err)
		}
		telemetryOptions = func() telemetry.Options { return o }
		if hookObserve() || !marked(o) || telemetry.Decide(o).Saved {
			t.Fatal("pre-consent day unmarked, or the hook enrolled")
		}
	})
	t.Run("CI alone marks nothing", func(t *testing.T) {
		_, o, _, set := hookTelemetryFixture(t, true)
		o.Getenv = env(map[string]string{"CI": "true"})
		set(o)
		if hookObserve() || marked(o) {
			t.Fatal("CI started a flush or marked a consenting install's day")
		}
	})
	t.Run("CI does not hide an opt-out", func(t *testing.T) {
		_, o, _, set := hookTelemetryFixture(t, true)
		if _, err := telemetry.SaveChoice(o, false, true); err != nil {
			t.Fatal(err)
		}
		o.Getenv = env(map[string]string{"CI": "true"})
		set(o)
		if hookObserve() || !marked(o) {
			t.Fatal("an explicit off behind CI left the day unmarked")
		}
	})
}

// Nothing in the telemetry steps may take the hook down: a non-zero exit denies the tool call.
func TestHookTelemetryNeverPanics(t *testing.T) {
	root, o, _, _ := hookTelemetryFixture(t, true)
	telemetryOptions = func() telemetry.Options { panic("telemetry state exploded") }
	out, sendable := hookAnswer("claude", "", hookPayload("s", root, "ls"), o.Now)
	if string(out) != "{}\n" || sendable {
		t.Fatalf("answer %q, sendable %v", out, sendable)
	}
	hookFlush()
}
