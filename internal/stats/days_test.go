package stats

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDaysPairingMatchesSummary(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 10, 1, 23, 59, 0, 0, time.FixedZone("offset", 3600))
	for _, e := range []Event{
		{TS: base.Add(3 * time.Hour), Session: "s", Block: "peek", Kind: KindCall}, // unsorted input
		{TS: base, Session: "s", Block: "peek", Kind: KindHint},
		{TS: base.Add(time.Minute), Session: "s", Block: "peek", Kind: KindHint},
		{TS: base.Add(4 * time.Hour), Session: "s", Block: "peek", Kind: KindCall},
		{TS: base, Block: "peek", Kind: KindHint},
		{TS: base.Add(time.Hour), Block: "peek", Kind: KindCall},
	} {
		if err := Append(dir, e); err != nil {
			t.Fatal(err)
		}
	}
	from, to := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	days, err := Days(dir, time.Time{}, from, to)
	if err != nil {
		t.Fatal(err)
	}
	// A hint from before the floor never pairs: the second day's follow of a first-day hint is gone
	// once the floor sits between them.
	next := from.AddDate(0, 0, 1)
	if floored, err := Days(dir, next, next, to); err != nil || floored["2026-10-02"].HintsFollowed != 0 || floored["2026-10-02"].Calls == 0 {
		t.Fatalf("floor: %+v %v", floored, err)
	}
	sum, err := Summarize(dir, 0, nil)
	if err != nil || days["2026-10-01"].Hints != 3 || days["2026-10-01"].HintsFollowed != 0 ||
		days["2026-10-02"].HintsFollowed != 1 || sum.HintsFollowed != 1 {
		t.Fatalf("pairing: %+v / %+v %v", days, sum, err)
	}
}

func TestDaysMissingAndSymlink(t *testing.T) {
	dir := t.TempDir()
	if got, err := Days(dir, time.Time{}, time.Time{}, time.Time{}); err != nil || len(got) != 0 {
		t.Fatalf("missing: %+v %v", got, err)
	}
	target := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(target, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "stats.jsonl")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := Days(dir, time.Time{}, time.Time{}, time.Time{}); err == nil {
		t.Fatal("Days followed symlink")
	}
	if _, err := Summarize(dir, 0, nil); err == nil {
		t.Fatal("Summarize followed symlink")
	}
}
