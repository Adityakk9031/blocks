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

// rollup scans at most seven complete UTC days, never one before the day the choice was saved, and
// returns them oldest first. through is empty if no marker advance is needed.
func rollup(o Options, s State) (events []Event, through string, err error) {
	today := o.now().Truncate(24 * time.Hour)
	from := today.AddDate(0, 0, -7)
	decided, err := time.Parse(time.RFC3339, s.DecidedAt)
	if err != nil {
		return nil, "", nil // no readable decision date: send no usage rather than guess
	}
	if d := decided.UTC().Truncate(24 * time.Hour); d.After(from) {
		from = d
	}
	if b, e := readSafe(o.Base, "telemetry-sent"); e == nil {
		if last, e := time.Parse(time.DateOnly, strings.TrimSpace(string(b))); e == nil {
			if next := last.AddDate(0, 0, 1); next.After(from) {
				from = next
			}
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
		fi, e := os.Lstat(filepath.Join(dir, "stats.jsonl"))
		if errors.Is(e, fs.ErrNotExist) || e == nil && !fi.Mode().IsRegular() {
			continue
		}
		if e != nil {
			return nil, "", e
		}
		counts, e := stats.Days(dir, from, today)
		if e != nil {
			return nil, "", e // never mark an incomplete scan as delivered
		}
		for day, count := range counts {
			if count.Events == 0 {
				continue
			}
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
