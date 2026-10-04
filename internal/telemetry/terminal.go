package telemetry

import "os"

// Interactive requires real terminals on both stdin and stderr; /dev/null is never a terminal.
func Interactive() bool { return isTerminal(os.Stdin.Fd()) && isTerminal(os.Stderr.Fd()) }

// agentEnv lists variables agent harnesses set in the shells they run.
var agentEnv = []string{"CLAUDECODE", "CODEX_SANDBOX", "CODEX_SANDBOX_NETWORK_DISABLED", "CODEX_THREAD_ID",
	"CURSOR_AGENT", "GEMINI_CLI", "OPENCODE", "AGENT"}

// InAgent reports whether a known agent harness runs this process. A harness can give its commands a
// pseudo-terminal, and the disclosure must reach a person, so such a run is never interactive.
// ponytail: a fixed list; a harness that sets none of these and allocates a terminal counts as a person.
func InAgent(getenv func(string) string) bool {
	for _, k := range agentEnv {
		if getenv(k) != "" {
			return true
		}
	}
	return false
}
