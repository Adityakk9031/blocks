package telemetry

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// Spawn starts a detached flush and releases it without waiting. Standard streams are disconnected.
func Spawn(events []Event) {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	cmd := exec.Command(exe, "telemetry", "flush")
	detach(cmd)
	cmd.Env = []string{}
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "CAVEMAN_BLOCKS_TELEMETRY_EVENTS=") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	if len(events) > 0 {
		b, err := json.Marshal(events)
		if err != nil {
			return
		}
		cmd.Env = append(cmd.Env, "CAVEMAN_BLOCKS_TELEMETRY_EVENTS="+string(b))
	}
	if cmd.Start() == nil {
		_ = cmd.Process.Release()
	}
}
