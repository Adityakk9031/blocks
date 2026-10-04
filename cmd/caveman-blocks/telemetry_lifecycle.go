package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"time"

	"github.com/caveman-ai/blocks/internal/scan"
	"github.com/caveman-ai/blocks/internal/telemetry"
	"github.com/spf13/cobra"
)

type telemetryContextKey struct{}

// commandTelemetry lives only for an eligible interactive invocation.
type commandTelemetry struct {
	options  telemetry.Options
	started  time.Time
	firstRun bool
	scan     telemetry.ScanCounts
	argv     []string
}

func telemetryInvocation(argv []string) bool {
	if len(argv) == 0 || slices.Contains([]string{"hook", "help", "version", "--version"}, argv[0]) ||
		len(argv) > 1 && argv[0] == "telemetry" && argv[1] == "flush" {
		return false
	}
	for _, arg := range argv {
		if arg == "--help" || arg == "-h" {
			return false
		}
	}
	return true
}

func beginTelemetry(o telemetry.Options, interactive bool, stderr io.Writer, argv []string) *commandTelemetry {
	if !interactive || !telemetryInvocation(argv) {
		return nil
	}
	t := &commandTelemetry{options: o, started: o.Now(), argv: argv}
	d := telemetry.Decide(o)
	if d.EnvOff && d.Saved && d.State.Enabled {
		if _, err := telemetry.SaveChoice(o, false, true); err == nil && d.State.AnonymousID != "" {
			fmt.Fprintln(stderr, telemetry.OffNotice(d.State.AnonymousID))
		}
	} else if argv[0] != "telemetry" && d.Status == "not started" {
		if s, err := telemetry.SaveChoice(o, true, false); err == nil {
			fmt.Fprintln(stderr, disclosure(s))
			t.firstRun = true
		}
	}
	return t
}

// disclosure is the documented line, plus the link notice when the install ID is shared.
func disclosure(s telemetry.State) string {
	if s.Linked {
		return telemetry.Disclosure + " · shares the Caveman CLI install ID"
	}
	return telemetry.Disclosure
}

func (t *commandTelemetry) end(c *cobra.Command, exit int) {
	d := telemetry.Decide(t.options)
	if !d.Sendable {
		return
	}
	e := telemetry.NewEvent(t.options, d.State, "command_run")
	e.Command = "unknown"
	if c != nil && c.Parent() != nil {
		path := strings.Fields(c.CommandPath())
		if len(path) > 1 {
			e.Command = path[1]
		}
		if len(path) > 2 {
			e.Subcommand = path[2]
		}
		positional := c.Flags().Args()
		if c.DisableFlagParsing {
			_, positional, _ = c.Root().Find(t.argv)
		}
		if (e.Command == "add" || e.Command == "run") && len(positional) > 0 && slices.Contains(t.options.FirstParty, positional[0]) {
			e.Block = positional[0]
		}
	}
	e.DurationMS = int(max(0, t.options.Now().Sub(t.started).Milliseconds()))
	e.ExitClass = "ok"
	if exit != 0 {
		e.ExitClass, e.ErrorClass = "error", "failed"
		if exit == 2 {
			e.ErrorClass = "usage"
		}
	} else if e.Command == "scan" {
		e.ScanCounts = t.scan
	}
	var events []telemetry.Event
	if t.firstRun {
		events = append(events, telemetry.NewEvent(t.options, d.State, "first_run"))
	}
	if e.Validate(t.options.FirstParty) == nil {
		events = append(events, e)
	}
	if len(events) > 0 {
		spawnTelemetry(events)
	}
}

func attachTelemetry(root *cobra.Command, t *commandTelemetry) {
	ctx := root.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	root.SetContext(context.WithValue(ctx, telemetryContextKey{}, t))
}

func recordScanTelemetry(c *cobra.Command, rep scan.Report, window time.Duration) {
	ctx := c.Context()
	if ctx == nil {
		return
	}
	t, _ := ctx.Value(telemetryContextKey{}).(*commandTelemetry)
	if t == nil {
		return
	}
	t.scan = telemetry.ScanCounts{ScanWindowDays: min(int(window/(24*time.Hour)), 3660), ScanCommands: rep.Commands,
		ScanScripts: rep.Scripts, ScanEdits: rep.Edits, ScanShapes: len(rep.Groups)}
	for _, group := range rep.Groups {
		if group.Count >= 2 {
			t.scan.ScanRepeatedShapes++
		}
		if len(group.CoveredBy) > 0 {
			t.scan.ScanCoveredShapes++
			t.scan.ScanCoveredScripts += group.Count
		}
		t.scan.ScanTopCount = max(t.scan.ScanTopCount, group.Count)
	}
}
