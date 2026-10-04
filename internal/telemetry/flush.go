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
	endpoint := o.Endpoint()
	if !d.Sendable || endpoint == "" {
		return
	}
	events := extraEvents(o.env("CAVEMAN_BLOCKS_TELEMETRY_EVENTS"), o, d.State)
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
		// -q, first: ~/.curlrc is not read, so nothing it holds (a header, credentials) rides along.
		// --url and --globoff: the destination is one URL, never an option or a pattern.
		// --noproxy: the plain-http test override to this machine never goes out through a proxy.
		cmd := exec.Command(curl, "-q", "--globoff", "--noproxy", "127.0.0.1,localhost", "--silent", "--show-error", "--fail", "--max-time", "10", "--request", "POST",
			"--header", "content-type: application/json", "--user-agent", "caveman-blocks/"+o.Version,
			"--data-binary", string(payload), "--url", endpoint)
		detach(cmd) // on Windows a console program started by a detached parent would open a window
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

// extraEvents decodes the events a command handed over. The parent's word is taken only for what
// happened: who sends it is rebuilt from the choice saved now, so an event never goes out under an
// install ID, version or platform other than this install's.
func extraEvents(raw string, o Options, s State) []Event {
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
		if dec.Decode(&event) != nil {
			continue
		}
		own := NewEvent(o, s, event.Event)
		event.Schema, event.AnonymousID, event.CLIVersion, event.OS, event.Arch, event.InstallChannel, event.Linked =
			own.Schema, own.AnonymousID, own.CLIVersion, own.OS, own.Arch, own.InstallChannel, own.Linked
		if event.Validate(o.FirstParty) == nil {
			events = append(events, event)
		}
	}
	return events
}
