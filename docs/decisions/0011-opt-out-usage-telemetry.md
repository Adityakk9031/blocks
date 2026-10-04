# 0011: Opt-out usage telemetry, counts only, sent by the system curl

Date: 2026-10-03. Status: accepted. Amends 0009's "no network calls at all" for telemetry only.

## Context

Blocks is free and has no account. The only evidence that it is used is what the hook and runner
count locally (decision 0005), and those counts never left the machine, so the roadmap's gates could
be measured on the maintainers' own repos only. Funding a free tool needs install, retention and usage
numbers. The Caveman CLI already ships opt-out telemetry with a reviewed set of controls and a
receiving Supabase project.

The hook sees every shell command an agent runs, so the bar is higher here than for a normal CLI.
Linking Go's `net/http` into the binary was measured: it moves the hook's rule-1 time from about
3.5 ms to about 5.5 ms, past the 5 ms budget, and grows the binary from 5.5 MB to 9.2 MB.

## Decision

Usage telemetry is on by default and opt-out, with the Caveman CLI's controls: a one-line disclosure
on the first interactive command, nothing sent before it, `caveman-blocks telemetry off`,
`DO_NOT_TRACK`, always off in CI, and commands without a terminal never save the default.

Only counts leave the machine. The event type holds numbers, booleans and strings from fixed lists; a
test fails when a free-text field is added. Script text, fingerprints, paths, repo names and the names
of user blocks are never sent; first-party block names are, because that list is public.

The binary still contains no network code. A detached `caveman-blocks telemetry flush` hands one JSON
payload to the system `curl`. The hook starts that process at most once a day, after its answer.

Events land in a new table in the Caveman CLI's Supabase project through a new Edge Function, with
the same rate limits and retention. The install ID is shared with the Caveman CLI when that CLI's
telemetry is on, and an opt-out saved by the Caveman CLI turns Blocks off too.

Contract and controls: [TELEMETRY.md](../TELEMETRY.md).

## Consequences

- "No telemetry" is no longer true and every document that said so changes with this record.
- 0009 holds where it matters: user blocks never leave the repo, and there is still no upload,
  publish or sync command.
- A machine without `curl` sends nothing. Accepted; a separate sender binary is the upgrade if that
  share matters.
- Only complete days are sent, so an install that is used for one day and never again reports its
  commands but not that day's usage. Accepted for v1.
- Off is kept per day: a day on which any process found telemetry off is never sent, so the hours of
  that day it was on are discarded with it. Accepted; it keeps "off" true without a per-event flag.
- The hook reads the saved choice on every pre-run call inside a repo, after its answer. Measured
  against the previous binary over 400 interleaved runs, the median did not move.
- Adding a first-party block means adding its name to the receiving function's allowlist, or its
  name is dropped from events.
- Reading `~/.caveman-cloud/config.json` is the one place this repository knows about the Caveman
  CLI (0008). It is a file read, not a dependency.
