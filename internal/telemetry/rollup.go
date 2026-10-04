package telemetry

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/caveman-ai/blocks/internal/stats"
)

// rollup counts the unsent complete UTC days, oldest first, at most seven back. Nothing from before
// floor is read at all. floor is the latest of: when telemetry last started (decided_at), the
// Caveman CLI's own decision when the choice follows that CLI, and the day after the last day any
// process saw telemetry off (NoteOff). through is empty if no marker advance is needed.
func rollup(o Options, s State) (events []Event, through string, err error) {
	today := o.now().Truncate(24 * time.Hour)
	floor, err := time.Parse(time.RFC3339, s.DecidedAt)
	if err != nil {
		return nil, "", nil // no readable decision time: send no usage rather than guess
	}
	floor = floor.UTC()
	if !s.Explicit {
		// The Caveman CLI's opt-out had Blocks off (control 6) until that CLI's latest decision.
		if c := cavemanChoice(o.Home); c.Enabled != nil {
			at, err := time.Parse(time.RFC3339, c.DecidedAt)
			if err != nil {
				return nil, "", nil
			}
			if at.After(floor) {
				floor = at.UTC()
			}
		}
	}
	// ponytail: a telemetry-off marker dated in the future (the clock was ahead while telemetry was
	// off) holds usage back until that date; failing closed beats sending an off day.
	if fi, e := os.Lstat(filepath.Join(o.Base, "telemetry-off")); e == nil {
		if !fi.Mode().IsRegular() {
			return nil, "", nil // a marker NoteOff cannot keep current (a symlink) says nothing: send no usage
		}
		if next := fi.ModTime().UTC().Truncate(24*time.Hour).AddDate(0, 0, 1); next.After(floor) {
			floor = next
		}
	}
	from := today.AddDate(0, 0, -7)
	if floor.After(from) {
		from = floor
	}
	if b, e := readSafe(o.Base, "telemetry-sent"); e == nil {
		// A watermark from the future is ignored: the receiver keeps one row per install and day.
		last, e := time.Parse(time.DateOnly, strings.TrimSpace(string(b)))
		if next := last.AddDate(0, 0, 1); e == nil && last.Before(today) && next.After(from) {
			from = next
		}
	} else if !errors.Is(e, fs.ErrNotExist) {
		return nil, "", e
	}
	if !from.Before(today) {
		return nil, "", nil
	}
	entries, err := os.ReadDir(o.Base)
	if err != nil {
		return nil, "", err
	}
	days := map[string]DailyCounts{}
	userNames := map[string]map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			continue
		}
		dir := filepath.Join(o.Base, entry.Name())
		if fi, e := os.Lstat(filepath.Join(dir, "stats.jsonl")); e != nil || !fi.Mode().IsRegular() {
			continue
		}
		counts, e := stats.Days(dir, floor, from, today)
		if e != nil {
			// ponytail: a repo whose stats cannot be read (a root-owned dir, one over-long line) is
			// left out of its days instead of stalling every repo's usage forever.
			continue
		}
		for day, count := range counts {
			d, exists := days[day]
			if !exists {
				d.FirstPartyRuns, d.HarnessSessions = map[string]int{}, map[string]int{}
				userNames[day] = map[string]bool{}
			}
			d.ReposActive++
			d.Sessions += count.Sessions
			d.Scripts += count.Scripts
			d.Hints += count.Hints
			d.HintsFollowed += count.HintsFollowed
			d.PromoteHints += count.PromoteHints
			d.Calls += count.Calls
			d.Runs += count.Runs
			d.RunsFailed += count.RunsFailed
			d.VerifyPass += count.VerifyPass
			d.VerifyFail += count.VerifyFail
			d.BytesFull += count.BytesFull
			d.BytesReturned += count.BytesReturned
			for name, n := range count.BlockRuns {
				if slices.Contains(o.FirstParty, name) {
					d.FirstPartyRuns[name] += n
				} else {
					d.RunsUser += n
					if name != "" {
						userNames[day][name] = true
					}
				}
			}
			for harness, n := range count.HarnessSessions {
				if mapValidators["HarnessSessions"](harness, nil) {
					d.HarnessSessions[harness] += n
				}
			}
			d.BlocksFirstParty, d.BlocksUser = len(d.FirstPartyRuns), len(userNames[day])
			days[day] = d
		}
	}
	var ordered []string
	for day := range days {
		ordered = append(ordered, day)
	}
	sort.Strings(ordered)
	for _, day := range ordered {
		e := NewEvent(o, s, "daily_usage")
		e.Day, e.DailyCounts = day, days[day]
		if err := e.Validate(o.FirstParty); err != nil {
			return nil, "", err
		}
		events = append(events, e)
	}
	return events, today.AddDate(0, 0, -1).Format(time.DateOnly), nil
}
