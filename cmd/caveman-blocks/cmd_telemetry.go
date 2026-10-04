package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/caveman-ai/blocks/blocks"
	"github.com/caveman-ai/blocks/internal/registry"
	"github.com/caveman-ai/blocks/internal/telemetry"
	"github.com/spf13/cobra"
)

// These seams keep tests from re-executing the test binary or reading a person's telemetry choice.
var spawnTelemetry = telemetry.Spawn

// telemetryInteractive reports a person at a terminal: real terminals on stdin and stderr, and no
// agent harness in between. Only such a run may save a yes.
var telemetryInteractive = func() bool { return telemetry.Interactive() && !telemetry.InAgent(os.Getenv) }
var telemetryOptions = func() telemetry.Options {
	names, _ := (registry.Registry{FS: blocks.FS}).List()
	return telemetry.System(version, names)
}

func telemetryCmd() *cobra.Command {
	c := &cobra.Command{Use: "telemetry", Short: "Show or change usage telemetry.", Args: args(cobra.NoArgs)}
	c.AddCommand(&cobra.Command{
		Use: "status", Short: "Show the effective choice, install ID and destination.", Args: args(cobra.NoArgs),
		Run: func(c *cobra.Command, _ []string) {
			o := telemetryOptions()
			d := telemetry.Decide(o)
			id, shared := d.State.AnonymousID, "no"
			if id == "" {
				id = "none"
			}
			if d.State.Linked && d.State.AnonymousID != "" {
				shared = "yes"
			}
			endpoint := o.Endpoint()
			if endpoint == "" {
				endpoint = "none; CAVEMAN_BLOCKS_TELEMETRY_URL is not an accepted https URL, so nothing is sent"
			}
			fmt.Fprintf(c.OutOrStdout(), "%s · %s\ninstall ID: %s\nshared with the Caveman CLI: %s\nendpoint: %s\n",
				d.Status, d.Reason, id, shared, endpoint)
		},
	}, &cobra.Command{
		Use: "on", Short: "Save an explicit choice to send usage counts.", Args: args(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			// A yes has to come from a person: an agent or a script can turn telemetry off, never on.
			if !telemetryInteractive() {
				return usageError{errors.New("telemetry on needs a person at a terminal; run it yourself, so you see what is sent")}
			}
			s, err := telemetry.SaveChoice(telemetryOptions(), true, true)
			if err != nil {
				return err
			}
			fmt.Fprintln(c.ErrOrStderr(), disclosure(s))
			return nil
		},
	}, &cobra.Command{
		Use: "off", Short: "Save an explicit opt-out and remove the install ID.", Args: args(cobra.NoArgs),
		RunE: func(c *cobra.Command, _ []string) error {
			o := telemetryOptions()
			d := telemetry.Decide(o)
			if _, err := telemetry.SaveChoice(o, false, true); err != nil {
				return err
			}
			if d.State.AnonymousID != "" {
				fmt.Fprintln(c.ErrOrStderr(), telemetry.OffNotice(d.State.AnonymousID))
			}
			return nil
		},
	}, &cobra.Command{
		Use: "flush", Hidden: true, Args: args(cobra.NoArgs),
		Run: func(_ *cobra.Command, _ []string) { telemetry.Flush(telemetryOptions()) },
	})
	return c
}
