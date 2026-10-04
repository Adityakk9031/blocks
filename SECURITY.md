# Security

Report vulnerabilities to security@caveman.so. Do not open a public issue. Expect an acknowledgement
within three business days.

Scope: the `caveman-blocks` binary, its hook adapters, the installer scripts, the npm packages and the
first-party blocks in `blocks/`.

Design boundaries are described in [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md#security-boundaries) and
decisions 0006 and 0009. In short: hooks install at user level only, never from repo configuration; the hook writes only to a
state directory outside the repo and refuses symlinks under `.blocks/`; declared effects are enforced in
the runner from committed config and `network` and `external` are denied by default; user blocks are
never uploaded anywhere; the binary contains no network code.

What this does NOT protect against: a malicious repository. A repo author writes the blocks, their
`effects`, their stamps and `allow_effects`, so in an untrusted clone "verified" means nothing and a
block is arbitrary code the same way a Makefile is. The hook never runs a block; it only hints. Treat
`caveman-blocks run` in a repo you do not trust like running that repo's scripts.

## Usage telemetry

On by default, opt-out, counts only: commands run, hints shown and followed, block runs, a random
install ID, OS and the sender's IP address. Never scripts, file paths, repo names or the names of blocks
you wrote. `caveman-blocks telemetry off` or `DO_NOT_TRACK=1` turns it off for good; it is always off in
CI, and nothing is sent before an interactive command has printed the disclosure. The payload is handed
to the system `curl` by a detached process, so the hook never opens a connection.

Every field, every control, where it is stored, retention and how to have it deleted:
[docs/TELEMETRY.md](docs/TELEMETRY.md). An event that carries anything that page does not list is a
vulnerability; report it as one.
