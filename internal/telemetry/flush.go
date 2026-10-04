package telemetry

import (
	"bytes"
	"encoding/json"
	"io"
	"os/exec"
	"strings"
)

// Flush is the detached child's entire job. Failures are silent; no network work runs in a caller.
// It never saves telemetry.json, so a concurrent off cannot be overwritten by an old on decision.
func Flush(o Options) {
	d := Decide(o)
	if !d.Sendable {
		return
	}
	events := extraEvents(o.env("CAVEMAN_BLOCKS_TELEMETRY_EVENTS"), o.FirstParty)
	daily, through, err := rollup(o, d.State)
	if err != nil {
		through = "" // commands can still be sent; incomplete days must be retried
	}
	events = append(events, daily...)
	if len(events) == 0 {
		advance(o, through)
		return
	}
	curl, err := exec.LookPath("curl")
	if err != nil {
		// ponytail: machines without curl send nothing; a separate sender binary is the upgrade.
		return
	}
	dailyOK := true
	dailyStart := len(events) - len(daily)
	for start := 0; start < len(events); start += 20 {
		current := Decide(o)
		if !current.Sendable || current.State.AnonymousID != d.State.AnonymousID {
			return
		}
		end := min(start+20, len(events))
		payload, err := json.Marshal(events[start:end])
		if err != nil || len(payload) == 0 || payload[0] != '[' {
			return // curl treats a leading @ as a local file; never let one reach --data-binary
		}
		cmd := exec.Command(curl, "--silent", "--show-error", "--fail", "--max-time", "10", "--request", "POST",
			"--header", "content-type: application/json", "--user-agent", "caveman-blocks/"+o.Version,
			"--data-binary", string(payload), o.Endpoint())
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if cmd.Run() != nil && end > dailyStart {
			dailyOK = false
		}
	}
	if dailyOK {
		advance(o, through)
	}
}

func advance(o Options, through string) {
	if through != "" {
		// Concurrent flushes can resend a day; the receiver keeps the first row per install and day.
		_ = writeSafe(o.Base, "telemetry-sent", []byte(through+"\n"))
	}
}

func extraEvents(raw string, firstParty []string) []Event {
	var items []json.RawMessage
	dec := json.NewDecoder(strings.NewReader(raw))
	if dec.Decode(&items) != nil || dec.Decode(new(any)) != io.EOF {
		return nil
	}
	var events []Event
	for _, item := range items {
		var event Event
		dec := json.NewDecoder(bytes.NewReader(item))
		dec.DisallowUnknownFields()
		if dec.Decode(&event) == nil && event.Validate(firstParty) == nil {
			events = append(events, event)
		}
	}
	return events
}
