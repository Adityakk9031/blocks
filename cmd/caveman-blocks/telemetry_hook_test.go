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
	invoke := func(harness, session string) {
		t.Helper()
		out, base := hookAnswer(harness, "", hookPayload(session, root, "caveman-blocks run json-peek"), o.Now)
		if string(out) != "{}\n" || base != o.Base {
			t.Fatalf("answer %s, base %q", out, base)
		}
		hookTelemetry()
	}
	invoke("codex", "s1")
	if len(*calls) != 1 || len((*calls)[0]) != 0 || telemetry.FlushDue(o.Base, o.Now()) {
		t.Fatalf("first daily attempt: %+v", *calls)
	}
	invoke("claude", "s2")
	if len(*calls) != 1 || marked(o) {
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
	invoke("claude", "s3")
	if len(*calls) != 1 || telemetry.FlushDue(o.Base, later) || !marked(o) {
		t.Fatal("off hook spawned, failed to touch the attempt marker, or left the day unmarked")
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

// Every hook call that finds telemetry off marks the day, whatever turned it off, so that day's
// counts are never sent once it is on again (internal/telemetry TestRollupSkipsOffDays).
func TestHookTelemetryMarksOffDays(t *testing.T) {
	t.Run("environment switch", func(t *testing.T) {
		_, o, calls, set := hookTelemetryFixture(t, true)
		o.Getenv = func(k string) string { return map[string]string{"DO_NOT_TRACK": "1"}[k] }
		set(o)
		hookTelemetry()
		fi, err := os.Stat(filepath.Join(o.Base, "telemetry-off"))
		if err != nil || !fi.ModTime().Equal(o.Now()) || len(*calls) != 0 {
			t.Fatalf("DO_NOT_TRACK: marker %v, started %+v", err, *calls)
		}
	})
	t.Run("the Caveman CLI opt-out, later removed", func(t *testing.T) {
		_, o, calls, _ := hookTelemetryFixture(t, false)
		config := filepath.Join(o.Home, ".caveman-cloud", "config.json")
		if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(config, []byte(`{"telemetry":{"enabled":false,"decidedAt":"2026-10-04T08:00:00Z","promptVersion":5}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		hookTelemetry()
		if !marked(o) || len(*calls) != 0 {
			t.Fatalf("opt-out not marked, or flush started: %+v", *calls)
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
		o, _, calls := cliTelemetry(t)
		var err error
		if o.Base, err = repo.StateBase(); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(o.Base, 0o700); err != nil {
			t.Fatal(err)
		}
		telemetryOptions = func() telemetry.Options { return o }
		hookTelemetry()
		if !marked(o) || len(*calls) != 0 || telemetry.Decide(o).Saved {
			t.Fatal("pre-consent day unmarked, or the hook enrolled")
		}
	})
}

// Nothing in the telemetry step may take the hook down: a non-zero exit denies the tool call.
func TestHookTelemetryNeverPanics(t *testing.T) {
	hookTelemetryFixture(t, true)
	telemetryOptions = func() telemetry.Options { panic("telemetry state exploded") }
	hookTelemetry()
}
