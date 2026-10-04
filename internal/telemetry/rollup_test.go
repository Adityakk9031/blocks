package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/caveman-ai/blocks/internal/stats"
)

// usageFixture deliberately carries private names, sessions and fingerprints in both repos.
func usageFixture(t *testing.T, o Options) {
	t.Helper()
	yes, no := true, false
	for _, repoName := range []string{"private-repo-one", "private-repo-two"} {
		dir := filepath.Join(o.Base, repoName)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		at := func(day, hour int) time.Time { return time.Date(2026, 10, day, hour, 0, 0, 0, time.UTC) }
		for _, e := range []stats.Event{
			{TS: at(1, 1), Kind: stats.KindScript, Session: "secret-session", Harness: "claude", FP: "private-fingerprint"},
			{TS: at(1, 2), Kind: stats.KindHint, Session: "secret-session", Harness: "claude", Block: "json-peek"},
			{TS: at(1, 3), Kind: stats.KindHint, Session: "secret-session", Harness: "claude", Block: "json-peek"},
			{TS: at(1, 4), Kind: stats.KindCall, Session: "secret-session", Harness: "claude", Block: "json-peek"},
			{TS: at(1, 5), Kind: stats.KindCall, Session: "secret-session", Harness: "claude", Block: "json-peek"},
			{TS: at(1, 6), Kind: stats.KindCall, Session: "second-secret", Harness: "codex", Block: "test-summary"},
			{TS: at(1, 7), Kind: stats.KindHint, Session: "second-secret", Harness: "codex", Block: "test-summary"},
			{TS: at(1, 8), Kind: stats.KindCall, Session: "wrong-secret", Harness: "cursor", Block: "test-summary"},
			{TS: at(1, 9), Kind: stats.KindPromoteHint, Session: "secret-session", Harness: "generic"},
			{TS: at(1, 10), Kind: stats.KindRun, Block: "json-peek", OK: &yes, BytesFull: 5000, BytesReturned: 100},
			{TS: at(1, 11), Kind: stats.KindRun, Block: "acme-secret-report", OK: &no, BytesFull: 3000, BytesReturned: 50},
			{TS: at(1, 12), Kind: stats.KindRun, Block: "acme-secret-report", OK: &yes, BytesFull: 100, BytesReturned: 100},
			{TS: at(1, 13), Kind: stats.KindVerify, Block: "json-peek", OK: &yes},
			{TS: at(1, 14), Kind: stats.KindVerify, Block: "acme-secret-report", OK: &no},
			{TS: at(2, 1), Kind: stats.KindCall, Session: "second-secret", Harness: "codex", Block: "test-summary"},
			{TS: at(2, 2), Kind: stats.KindScript, Session: "secret-session", Harness: "not-allowlisted"},
			{TS: at(4, 1), Kind: stats.KindRun, Block: "today-private", OK: &yes},
			{TS: at(5, 1), Kind: stats.KindRun, Block: "future-private", OK: &yes},
			{TS: at(-5, 1), Kind: stats.KindRun, Block: "old-private", OK: &yes},
		} {
			if err := stats.Append(dir, e); err != nil {
				t.Fatal(err)
			}
		}
		f, err := os.OpenFile(filepath.Join(dir, "stats.jsonl"), os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		_, err = f.WriteString("malformed\n{\"ts\":\"bad\"}\n")
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestRollup(t *testing.T) {
	o := testOptions(t)
	usageFixture(t, o)
	if err := os.Symlink(filepath.Join(o.Base, "private-repo-one"), filepath.Join(o.Base, "symlink-repo")); err != nil {
		t.Logf("symlink fixture skipped: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(o.Base, "no-stats"), 0o700); err != nil {
		t.Fatal(err)
	}
	events, through, err := rollup(o, onState(true))
	if err != nil || through != "2026-10-03" || len(events) != 2 {
		t.Fatalf("rollup: %+v %s %v", events, through, err)
	}
	a, b := events[0], events[1]
	if a.Day != "2026-10-01" || a.ReposActive != 2 || a.Sessions != 6 || a.Scripts != 2 || a.Hints != 6 ||
		a.HintsFollowed != 2 || a.PromoteHints != 2 || a.Calls != 8 || a.Runs != 6 || a.RunsFailed != 2 ||
		a.RunsUser != 4 || a.BlocksUser != 1 || a.BlocksFirstParty != 1 || a.FirstPartyRuns["json-peek"] != 2 ||
		a.VerifyPass != 2 || a.VerifyFail != 2 || a.BytesFull != 16200 || a.BytesReturned != 500 ||
		a.HarnessSessions["claude"] != 2 || a.HarnessSessions["codex"] != 2 || a.HarnessSessions["cursor"] != 2 || a.HarnessSessions["generic"] != 2 {
		t.Fatalf("first day: %+v", a)
	}
	if b.Day != "2026-10-02" || b.HintsFollowed != 2 || b.Calls != 2 || b.Sessions != 4 || len(b.HarnessSessions) != 1 {
		t.Fatalf("cross-day pairing: %+v", b)
	}
	put(t, filepath.Join(o.Base, "telemetry-sent"), "2026-10-01\n")
	events, _, err = rollup(o, onState(true))
	if err != nil || len(events) != 1 || events[0].HintsFollowed != 2 {
		t.Fatalf("hint before from must pair: %+v %v", events, err)
	}
	put(t, filepath.Join(o.Base, "telemetry-sent"), "2026-10-03\n")
	if events, through, err = rollup(o, onState(true)); err != nil || len(events) != 0 || through != "" {
		t.Fatalf("already sent: %+v %s %v", events, through, err)
	}
}

// Days before the day the choice was saved are never sent, whatever stats.jsonl holds.
func TestRollupNeverBeforeDecision(t *testing.T) {
	o := testOptions(t)
	usageFixture(t, o)
	s := onState(false)
	s.DecidedAt = "2026-10-02T15:00:00Z"
	events, through, err := rollup(o, s)
	if err != nil || through != "2026-10-03" || len(events) != 1 || events[0].Day != "2026-10-02" {
		t.Fatalf("decided on the 2nd: %+v %s %v", events, through, err)
	}
	s.DecidedAt = "2026-10-04T09:00:00Z" // today: nothing is complete yet
	if events, through, err = rollup(o, s); err != nil || len(events) != 0 || through != "" {
		t.Fatalf("decided today: %+v %s %v", events, through, err)
	}
	s.DecidedAt = ""
	if events, through, err = rollup(o, s); err != nil || len(events) != 0 || through != "" {
		t.Fatalf("no decision date: %+v %s %v", events, through, err)
	}
}
