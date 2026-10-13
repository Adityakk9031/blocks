#!/usr/bin/env bash
# Smoke test of `hooks install|status|uninstall` with the real binary on this OS, against throwaway
# home and config directories. CI runs it on Windows, where the Go tests' unix paths do not apply.
# usage: scripts/smoke-hooks.sh <path to caveman-blocks binary>
set -euo pipefail
bin=$1
tmp=$(mktemp -d)
if command -v cygpath >/dev/null; then tmp=$(cygpath -m "$tmp"); fi # Git Bash: a path the native binary reads
trap 'rm -rf "$tmp"' EXIT
export HOME="$tmp/home" USERPROFILE="$tmp/home" XDG_STATE_HOME="$tmp/state" CAVEMAN_BLOCKS_TELEMETRY=off
export CLAUDE_CONFIG_DIR="$tmp/claude config" # a space: the hook command must be quoted
mkdir -p "$HOME" "$CLAUDE_CONFIG_DIR"
settings="$CLAUDE_CONFIG_DIR/settings.json"
fail() { echo "smoke-hooks: $*" >&2; [ ! -f "$settings" ] || cat "$settings" >&2; exit 1; }
# tr: jq on Windows may end lines with CRLF.
entries() { jq '[.hooks.PreToolUse[]?.hooks[]?.command] | length' "$settings" | tr -d '\r'; }
status() { "$bin" hooks status --harness claude-code --json | jq -r ".harnesses[0].$1" | tr -d '\r'; }

"$bin" hooks install --harness claude-code
[ "$(entries)" = 1 ] || fail "want one entry after install"
[ "$(status installed)" = true ] || fail "status does not see the entry install wrote"
[ "$(status binary_present)" = true ] || fail "status: binary missing"
copy=$(status binary)
case "$(uname -s)" in MINGW* | MSYS* | CYGWIN*) [ "${copy%.exe}" != "$copy" ] || fail "installed copy has no .exe: $copy" ;; esac

# The command as written must run under the shell Claude Code uses (Git Bash on Windows).
cmd=$(jq -r '.hooks.PreToolUse[0].hooks[0].command' "$settings" | tr -d '\r')
echo '{}' | bash -c "$cmd" >/dev/null || fail "hook command does not run: $cmd"

out=$("$bin" hooks install --harness claude-code)
grep -q '^binary: .*(unchanged)$' <<<"$out" || fail "second install copied the binary again"
[ "$(entries)" = 1 ] || fail "second install duplicated the entry"

# v0.1.0-rc.4 on Windows wrote the entry without .exe: the next install replaces it, not adds to it.
if [ "${copy%.exe}" != "$copy" ]; then
  jq '.hooks.PreToolUse[0].hooks[0].command |= sub("\\.exe'"'"' hook"; "'"'"' hook")' "$settings" >"$settings.new" && mv "$settings.new" "$settings"
  grep -q "caveman-blocks' hook" "$settings" || fail "could not plant the rc.4 entry"
  [ "$(status installed)" = true ] || fail "rc.4 entry not recognised"
  "$bin" hooks install --harness claude-code
  [ "$(entries)" = 1 ] || fail "install over an rc.4 entry duplicated it"
  [ "$(status binary)" = "$copy" ] || fail "rc.4 entry not replaced"
fi

out=$("$bin" hooks uninstall --harness claude-code)
grep -q 'removed from' <<<"$out" || fail "uninstall found nothing to remove"
[ "$(status installed)" = false ] || fail "entry still there after uninstall"
[ ! -e "$HOME/.claude" ] || fail "wrote under ~/.claude despite CLAUDE_CONFIG_DIR"
echo "smoke-hooks: ok"
