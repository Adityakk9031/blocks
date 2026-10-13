//go:build e2e

package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/caveman-ai/blocks/internal/telemetry"
	"github.com/rogpeppe/go-internal/testscript"
)

// TestMain lets the test binary act as caveman-blocks when invoked under that name, so the
// scenarios run the real CLI without a separate build.
//
// The unit tests' init stubs the detached flush. A scenario that names a test receiver
// (CAVEMAN_BLOCKS_TELEMETRY_URL) gets the real one, so what a command spawns can be asserted;
// without that override nothing is spawned, so no scenario can reach the documented receiver.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == "caveman-blocks" && os.Getenv("CAVEMAN_BLOCKS_TELEMETRY_URL") != "" {
		spawnTelemetry = telemetry.Spawn
	}
	testscript.Main(m, map[string]func(){"caveman-blocks": main})
}

// TestE2E runs the testscript scenarios in testdata/e2e.
func TestE2E(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "../../testdata/e2e",
		Setup: func(env *testscript.Env) error {
			for k, v := range map[string]string{
				"GIT_AUTHOR_NAME": "e2e", "GIT_AUTHOR_EMAIL": "e2e@example.com",
				"GIT_COMMITTER_NAME": "e2e", "GIT_COMMITTER_EMAIL": "e2e@example.com",
				"GIT_CONFIG_NOSYSTEM": "1",
			} {
				env.Setenv(k, v)
			}
			// PLUGINSRC is the Codex plugin in the repo, for codex-plugin.txtar.
			src, err := filepath.Abs("../../plugins/caveman-blocks")
			if err != nil {
				return err
			}
			env.Setenv("PLUGINSRC", src)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			// cpbin dst: copy this binary to dst, where it still runs as caveman-blocks.
			"cpbin": func(ts *testscript.TestScript, neg bool, args []string) {
				if neg || len(args) != 1 {
					ts.Fatalf("usage: cpbin dst")
				}
				exe, err := os.Executable()
				ts.Check(err)
				dst := ts.MkAbs(args[0])
				ts.Check(os.MkdirAll(filepath.Dir(dst), 0o755))
				in, err := os.Open(exe)
				ts.Check(err)
				defer in.Close()
				out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
				ts.Check(err)
				_, err = io.Copy(out, in)
				ts.Check(err)
				ts.Check(out.Close())
			},
		},
	})
}
