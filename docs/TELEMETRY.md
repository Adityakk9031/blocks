# Telemetry

Blocks is free. It stays free because we can show it gets used, so the CLI sends usage counts by
default. This page lists everything that is sent, when, and how to turn it off. Rationale: decision
[0011](decisions/0011-opt-out-usage-telemetry.md).

```bash
caveman-blocks telemetry off      # off for good on this machine
caveman-blocks telemetry status   # on or off, why, and your install ID
caveman-blocks telemetry on
```

## Never sent

Script text, shape fingerprints, commands your agent ran, file paths, repo names or remotes, block
parameters, block output, and the names of blocks you wrote. The event type has no field that could
carry them: every field is a number, a boolean, or a string checked against a fixed list or a fixed
format (a timestamp, a UUID, a date, a version), in `internal/telemetry`, guarded by a test.

Names of first-party blocks (`json-peek`, `test-summary` and the rest of [blocks/](../blocks/README.md))
are sent, because that list is public and fixed.

## Controls, in precedence order

1. `DO_NOT_TRACK` set to anything but empty or `0`: off.
2. `CAVEMAN_BLOCKS_TELEMETRY` set to anything but empty, `1`, `true` or `on`: off.
3. `CAVEMAN_TELEMETRY`, the Caveman CLI's switch, set the same way: off. It can turn Blocks off, never on.
4. `CI` set to anything but empty, `0` or `false`: off.
5. A choice you made with `caveman-blocks telemetry on|off`. An `off` is honored forever.
6. An opt-out saved by the Caveman CLI (`~/.caveman-cloud/config.json`): off.
7. The default, on. It takes effect only after an interactive command (stdin and stderr are a
   terminal) has printed the disclosure line and saved the choice. Until then nothing is sent, and a
   command without a terminal (run by an agent, in a pipe, by the hook) never saves the default.

Off covers what was counted while it was off. Usage from before the choice was saved, from before the
Caveman CLI's latest decision when Blocks follows that CLI (control 6), or from a day on which any
`caveman-blocks` process saw switch 1 to 4, is never sent later.

Only a person at a terminal can turn telemetry on: `telemetry on` refuses anywhere else. An agent or a
script can turn it off. A `telemetry.json` that cannot be read counts as off and is never replaced by
the default.

When an interactive command sees switch 1, 2 or 3 while the saved choice is on, it saves a lasting
`off`. Hooks started by desktop apps often never read your shell profile, so the environment variable
alone would not reach them.

The disclosure line, printed once on stderr before the first interactive command runs:

```text
usage counts on · commands, hints shown and followed, block runs, a random install ID, OS and your IP address · never scripts, file paths, repo names or names of blocks you wrote · this is what keeps Blocks free · off: caveman-blocks telemetry off · https://github.com/caveman-ai/blocks/blob/main/docs/TELEMETRY.md
```

`help`, `version`, `telemetry status|off` and the hook never print it and never save the default.
`telemetry on` prints it. A command run by a known agent harness (`CLAUDECODE`, `CODEX_SANDBOX`,
`CODEX_SANDBOX_NETWORK_DISABLED`, `CODEX_THREAD_ID`, `CURSOR_AGENT`, `GEMINI_CLI`, `OPENCODE` or `AGENT` is
set) is never interactive, even when the harness gives it a terminal: the line has to reach a person.

## Install ID

A random UUID, saved with the choice. If the Caveman CLI is installed with its telemetry on, Blocks
reuses that CLI's install ID, so the two products can be counted as one install; the disclosure line
then also says `shares the Caveman CLI install ID`. The link is decided when the ID is saved. If the
Caveman CLI later turns its telemetry off or replaces its ID, Blocks stops using the shared ID at once:
nothing is sent until the next interactive command saves a new ID and prints the disclosure again.
`telemetry off` deletes the ID from disk and
prints it once, because it is the only key to a deletion request.

This is pseudonymous, not anonymous: the ID links one install's events together, and the stored IP
address shows where they came from.

## Events

Wire contract `blocks/v1`. One HTTPS `POST` carries a JSON array of at most 20 events.

Every event:

| Field | Type | Values |
|---|---|---|
| `schema` | string | `blocks/v1` |
| `event` | string | `first_run`, `command_run`, `daily_usage` |
| `ts` | string | RFC 3339, UTC, when the event was built |
| `anonymous_id` | string | the install ID |
| `cli_version` | string | `[A-Za-z0-9._+-]{1,64}` |
| `os`, `arch` | string | Go's `GOOS` and `GOARCH` |
| `install_channel` | string | `npx`, `brew`, `direct`, `source`; decided once, when the choice is saved |
| `linked` | bool | the install ID is shared with the Caveman CLI |

`first_run`: sent once per install ID, after the command that saved it. No further fields.

`command_run`: sent after each interactive command. Commands run by agents or scripts send none.

| Field | Type | Values |
|---|---|---|
| `command` | string | `init`, `hooks`, `add`, `run`, `lint`, `verify`, `sync`, `scan`, `promote`, `retire`, `export`, `stats`, `doctor`, `telemetry`, `unknown` |
| `subcommand` | string | empty; `install`, `uninstall` or `status` with `hooks`; `on`, `off` or `status` with `telemetry` |
| `block` | string | for `add` and `run`: the block name if it is a first-party name, else absent |
| `duration_ms` | int | |
| `exit_class` | string | `ok`, `error` |
| `error_class` | string | empty, `usage` (exit 2), `failed` (any other non-zero exit) |
| `scan_window_days` | int | `scan` only, and only when it succeeded: the `--since` window |
| `scan_commands` | int | shell commands read |
| `scan_scripts` | int | inline scripts found |
| `scan_edits` | int | scripts that edit a file in place |
| `scan_shapes` | int | distinct script shapes |
| `scan_repeated_shapes` | int | shapes seen at least twice |
| `scan_covered_shapes` | int | shapes a block covers |
| `scan_covered_scripts` | int | scripts in those shapes |
| `scan_top_count` | int | size of the largest shape |

`daily_usage`: one per install per UTC day with activity, summed over every repo on the machine, read
from the `stats.jsonl` files the hook and runner already write. Only complete past days are sent,
oldest first, at most 7 days back, and never activity from a time telemetry was off (see Controls).

| Field | Type | Meaning |
|---|---|---|
| `day` | string | `YYYY-MM-DD`, the UTC day counted |
| `repos_active` | int | repos with at least one event that day |
| `sessions` | int | agent sessions the hook saw; a session that works in two repos counts once per repo |
| `scripts` | int | inline scripts captured |
| `hints` | int | hints shown |
| `hints_followed` | int | hints followed by a call of the hinted block in the same session |
| `promote_hints` | int | promotion suggestions shown |
| `calls` | int | block calls the hook saw |
| `runs` | int | block runs |
| `runs_failed` | int | runs that exited non-zero |
| `runs_user` | int | runs of blocks that are not first-party |
| `verify_pass`, `verify_fail` | int | verify outcomes |
| `bytes_full`, `bytes_returned` | int | output bytes blocks produced, and bytes returned to the agent |
| `blocks_first_party` | int | distinct first-party blocks run |
| `blocks_user` | int | distinct other blocks run; a count, the names stay on the machine |
| `first_party_runs` | object | first-party block name to runs |
| `harness_sessions` | object | harness (`claude`, `codex`, `cursor`, `generic`) to distinct sessions |

## When it is sent, and how

- After an interactive command, the CLI starts a detached `caveman-blocks telemetry flush` and exits.
  That process sends the command's events plus any unsent complete days.
- The hook, once it has written its answer on a pre-run call inside a repo, checks one marker file
  and, at most once per 24 hours, starts the same detached process. The hook itself opens no
  connection.
- The binary contains no network code. `telemetry flush` hands the JSON to the system `curl`
  (`curl -q --max-time 10`; `-q` keeps `~/.curlrc` out of it); a machine without `curl` sends nothing.
  A failed send is dropped and never fails or slows a command; unsent days are retried on the next
  flush.
- `CAVEMAN_BLOCKS_TELEMETRY_URL` overrides the destination, for testing. It must be an `https://` URL,
  or `http://` to `127.0.0.1` or `localhost`; with anything else nothing is sent.

Files, all under `$XDG_STATE_HOME/caveman-blocks/` (default `~/.local/state/caveman-blocks/`):

| File | Holds |
|---|---|
| `telemetry.json` | the saved choice, install ID, install channel. Written by an interactive command, or by `telemetry off` |
| `telemetry-attempt` | empty; its mtime is the hook's last daily check |
| `telemetry-off` | empty; its mtime is the last day an environment switch had telemetry off |
| `telemetry-sent` | the last UTC day whose `daily_usage` was delivered |

## Where it goes, how long it stays

`https://xvfgtprkhzlvegvmeefq.supabase.co/functions/v1/blocks-telemetry`, a Supabase Edge Function in
the same project as the Caveman CLI's telemetry. It validates each event against this contract, drops
anything else, and stores the fields above with the time it received them and the IP address the
request came from, as seen by Supabase's edge. The IP is used to rate-limit each sender. One
`daily_usage` row is kept per install and day, the first to arrive. Source and
table schema: [`supabase/` in the caveman repo](https://github.com/JuliusBrussee/caveman/tree/main/supabase).

IP addresses are cleared from stored events after 90 days. Events are deleted 13 months after they
were received. Supabase's own platform request logs record each request's IP address for that plan's
log retention period; the 90-day clearing covers the events table, not those logs.

The data is used to count installs, retention and how much blocks get used, and to choose what to
build next. It is not sold.

## Delete what was sent

`caveman-blocks telemetry status` shows your install ID while one is saved. `telemetry off` prints
it once as it deletes it. Send the ID to security@caveman.so with "telemetry deletion" in the subject
and every stored event for it is deleted.
