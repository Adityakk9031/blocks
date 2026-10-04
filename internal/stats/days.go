package stats

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/caveman-ai/blocks/internal/repo"
)

// Day holds local measured counts. Block names and session IDs never cross the wire from here.
type Day struct {
	Events, Sessions, Scripts, Hints, HintsFollowed, PromoteHints int
	Calls, Runs, RunsFailed, VerifyPass, VerifyFail               int
	BytesFull, BytesReturned                                      int
	HarnessSessions, BlockRuns                                    map[string]int
}

// Days counts UTC days in [from, to), keyed by YYYY-MM-DD. A followed hint is credited on the
// call's day; pairing spans midnight and reads earlier hints, including ones before from.
// Missing files are empty; malformed lines are skipped; symlinks are refused.
func Days(stateDir string, from, to time.Time) (map[string]Day, error) {
	events, err := readEvents(stateDir, time.Time{}, to)
	if err != nil {
		return nil, err
	}
	days := map[string]Day{}
	sessions := map[string]map[string]bool{}
	harnesses := map[string]map[[2]string]bool{}
	pending := hintPairs{}
	for _, e := range events {
		followed := pending.followed(e)
		if e.TS.Before(from) {
			continue
		}
		key := e.TS.UTC().Format(time.DateOnly)
		d, exists := days[key]
		if !exists {
			d.HarnessSessions, d.BlockRuns = map[string]int{}, map[string]int{}
			sessions[key], harnesses[key] = map[string]bool{}, map[[2]string]bool{}
		}
		d.Events++
		if e.Session != "" {
			sessions[key][e.Session] = true
			hk := [2]string{e.Harness, e.Session}
			if e.Harness != "" && !harnesses[key][hk] {
				harnesses[key][hk] = true
				d.HarnessSessions[e.Harness]++
			}
		}
		if followed {
			d.HintsFollowed++
		}
		switch e.Kind {
		case KindScript:
			d.Scripts++
		case KindHint:
			d.Hints++
		case KindPromoteHint:
			d.PromoteHints++
		case KindCall:
			d.Calls++
		case KindRun:
			d.Runs++
			d.BlockRuns[e.Block]++
			if e.OK != nil && !*e.OK {
				d.RunsFailed++
			}
			d.BytesFull += e.BytesFull
			d.BytesReturned += e.BytesReturned
		case KindVerify:
			if e.OK != nil && *e.OK {
				d.VerifyPass++
			} else {
				d.VerifyFail++
			}
		}
		d.Sessions = len(sessions[key])
		days[key] = d
	}
	return days, nil
}

// hintPairs owns the pairing rule shared by Summarize and Days. Repeated hints replace the
// unanswered hint; only the next call in that session earns credit. Stable timestamp order wins ties.
type hintPairs map[[2]string]bool

func (p hintPairs) followed(e Event) bool {
	if e.Session == "" || e.Block == "" {
		return false
	}
	k := [2]string{e.Session, e.Block}
	if e.Kind == KindHint {
		p[k] = true
	} else if e.Kind == KindCall && p[k] {
		delete(p, k)
		return true
	}
	return false
}

func readEvents(stateDir string, from, to time.Time) ([]Event, error) {
	p := filepath.Join(stateDir, "stats.jsonl")
	fi, err := os.Lstat(p)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("stats.jsonl: not a regular file")
	}
	f, err := os.OpenFile(p, os.O_RDONLY|repo.NoFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var events []Event
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e Event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.TS.IsZero() || e.TS.Before(from) || !to.IsZero() && !e.TS.Before(to) {
			continue
		}
		events = append(events, e)
	}
	sort.SliceStable(events, func(i, j int) bool { return events[i].TS.Before(events[j].TS) })
	return events, sc.Err()
}
