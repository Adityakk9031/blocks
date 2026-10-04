package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testID = "12345678-1234-4123-8123-123456789abc"

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{Base: filepath.Join(t.TempDir(), "state"), Home: t.TempDir(), Executable: "/opt/bin/caveman-blocks",
		Version: "1.2.3", FirstParty: []string{"json-peek", "test-summary"},
		Getenv: func(string) string { return "" },
		Now:    func() time.Time { return time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC) }}
}

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func saved(t *testing.T, o Options, s State) {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	put(t, filepath.Join(o.Base, "telemetry.json"), string(b))
}

func onState(explicit bool) State {
	return State{Enabled: true, Explicit: explicit, DecidedAt: "2026-10-01T00:00:00Z",
		Disclosure: 1, AnonymousID: testID, InstallChannel: "direct"}
}

func TestDecisionPrecedence(t *testing.T) {
	envs := []struct {
		name, reason string
		values       map[string]string
		envOff       bool
	}{
		{"unset", "", nil, false},
		{"DNT dominates", "DO_NOT_TRACK", map[string]string{"DO_NOT_TRACK": "false", "CI": "true", "CAVEMAN_BLOCKS_TELEMETRY": "off"}, true},
		{"DNT zero", "", map[string]string{"DO_NOT_TRACK": "0"}, false},
		{"DNT whitespace", "DO_NOT_TRACK", map[string]string{"DO_NOT_TRACK": " "}, true},
		{"Blocks dominates", "CAVEMAN_BLOCKS_TELEMETRY", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": "0", "CAVEMAN_TELEMETRY": "off", "CI": "true"}, true},
		{"Blocks typo", "CAVEMAN_BLOCKS_TELEMETRY", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": "yes"}, true},
		{"Blocks whitespace", "CAVEMAN_BLOCKS_TELEMETRY", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": " "}, true},
		{"Blocks true no effect", "", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": " TRUE "}, false},
		{"Blocks one no effect", "", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": "1"}, false},
		{"Caveman dominates CI", "CAVEMAN_TELEMETRY", map[string]string{"CAVEMAN_BLOCKS_TELEMETRY": "on", "CAVEMAN_TELEMETRY": "off", "CI": "true"}, true},
		{"Caveman on no effect", "", map[string]string{"CAVEMAN_TELEMETRY": " On "}, false},
		{"CI true", "CI", map[string]string{"CI": "true"}, false},
		{"CI arbitrary", "CI", map[string]string{"CI": "anything"}, false},
		{"CI false", "", map[string]string{"CI": "FALSE"}, false},
		{"CI zero", "", map[string]string{"CI": "0"}, false},
	}
	for _, env := range envs {
		for _, choice := range []string{"absent", "default", "on", "off"} {
			for _, cave := range []string{"absent", "on", "off"} {
				t.Run(env.name+"/"+choice+"/caveman="+cave, func(t *testing.T) {
					o := testOptions(t)
					o.Getenv = func(k string) string { return env.values[k] }
					if choice != "absent" {
						s := onState(choice != "default")
						if choice == "off" {
							s.Enabled, s.AnonymousID = false, ""
						}
						saved(t, o, s)
					}
					if cave != "absent" {
						put(t, filepath.Join(o.Home, ".caveman-cloud/config.json"), `{"telemetry":{"enabled":`+map[bool]string{true: "true", false: "false"}[cave == "on"]+`,"decidedAt":"yes","promptVersion":1}}`)
					}
					want, reason := "not started", "the default (awaiting an interactive command)"
					if env.reason != "" {
						want, reason = "off", env.reason
					} else if choice == "on" || choice == "off" {
						want, reason = choice, "your choice"
					} else if cave == "off" {
						want, reason = "off", "the Caveman CLI opt-out"
					} else if choice == "default" {
						want, reason = "on", "the default"
					}
					d := Decide(o)
					if d.Status != want || d.Reason != reason || d.Sendable != (want == "on") || d.EnvOff != env.envOff {
						t.Fatalf("got %+v, want %s / %s", d, want, reason)
					}
				})
			}
		}
	}
}

func TestInvalidChoicesAndSendability(t *testing.T) {
	o := testOptions(t)
	put(t, filepath.Join(o.Base, "telemetry.json"), `{"enabled":false,"explicit":true}`)
	if d := Decide(o); d.Status != "off" || !d.Saved || d.Reason != "your choice" {
		t.Fatalf("explicit off does not require an ID or disclosure: %+v", d)
	}
	for _, raw := range []string{`{`, `null`, `[]`, `{"telemetry":{"enabled":"false","decidedAt":"yes"}}`,
		`{"telemetry":{"enabled":false}}`, `{"telemetry":{"enabled":null,"decidedAt":"yes"}}`} {
		o := testOptions(t)
		put(t, filepath.Join(o.Home, ".caveman-cloud/config.json"), raw)
		if d := Decide(o); d.Status != "not started" {
			t.Errorf("invalid config %q used: %+v", raw, d)
		}
	}
	for _, mutate := range []func(*State){
		func(s *State) { s.AnonymousID = "" }, func(s *State) { s.AnonymousID = "bad" },
		func(s *State) { s.Disclosure = 0 }, func(s *State) { s.Enabled = false },
	} {
		o, s := testOptions(t), onState(true)
		mutate(&s)
		saved(t, o, s)
		if Decide(o).Sendable {
			t.Errorf("invalid saved state sendable: %+v", s)
		}
	}
}

func TestSaveLinkOffAndChannels(t *testing.T) {
	for _, tc := range []struct{ exe, version, channel string }{
		{"/cache/_npx/x/bin", "0.0.0-dev", "npx"}, {"/opt/homebrew/bin/caveman-blocks", "1", "brew"},
		{"/opt/Cellar/blocks/bin", "1", "brew"}, {"/opt/Caskroom/blocks/bin", "1", "brew"},
		{"/opt/linuxbrew/bin/blocks", "1", "brew"}, {"/src/blocks", "0.0.0-dev", "source"}, {"/opt/blocks", "1", "direct"},
	} {
		t.Run(tc.channel+tc.exe, func(t *testing.T) {
			o := testOptions(t)
			o.Executable, o.Version = tc.exe, tc.version
			put(t, filepath.Join(o.Home, ".caveman-cloud/config.json"), `{"telemetry":{"enabled":true,"decidedAt":"yes","anonymousId":"`+strings.ToUpper(testID)+`"}}`)
			s, err := SaveChoice(o, true, false)
			if err != nil || s.AnonymousID != testID || !s.Linked || s.Explicit || s.InstallChannel != tc.channel {
				t.Fatalf("save: %+v %v", s, err)
			}
			o.Executable = "/changed/_npx/exe"
			s2, err := SaveChoice(o, true, true)
			if err != nil || s2.AnonymousID != s.AnonymousID || s2.InstallChannel != s.InstallChannel || !s2.Explicit {
				t.Fatalf("repeat on: %+v %v", s2, err)
			}
			if _, err := SaveChoice(o, false, true); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(o.Base, "telemetry.json"))
			if err != nil || strings.Contains(string(data), "anonymous_id") || strings.Contains(string(data), testID) {
				t.Fatalf("ID not removed: %s %v", data, err)
			}
			fi, err := os.Stat(filepath.Join(o.Base, "telemetry.json"))
			if err != nil || fi.Mode().Perm() != 0o600 {
				t.Fatalf("state mode: %v %v", fi, err)
			}
		})
	}
	for _, cave := range []string{``, `{"telemetry":{"enabled":false,"decidedAt":"yes","anonymousId":"` + testID + `"}}`,
		`{"telemetry":{"enabled":true,"decidedAt":"yes","anonymousId":"bad"}}`} {
		o := testOptions(t)
		put(t, filepath.Join(o.Home, ".caveman-cloud/config.json"), cave)
		s, err := SaveChoice(o, true, true)
		if err != nil || !validUUID(s.AnonymousID) || s.AnonymousID[14] != '4' || !strings.ContainsRune("89ab", rune(s.AnonymousID[19])) || s.Linked {
			t.Fatalf("mint: %+v %v", s, err)
		}
	}
}

func TestStateAndMarkerRefuseSymlinks(t *testing.T) {
	for _, name := range []string{"telemetry.json", "telemetry-attempt", "telemetry-sent"} {
		t.Run(name, func(t *testing.T) {
			o := testOptions(t)
			put(t, filepath.Join(o.Base, "victim"), "unchanged")
			if err := os.Symlink(filepath.Join(o.Base, "victim"), filepath.Join(o.Base, name)); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			if err := writeSafe(o.Base, name, []byte("bad")); err == nil {
				t.Fatal("wrote symlink")
			}
			if _, err := readSafe(o.Base, name); err == nil {
				t.Fatal("read symlink")
			}
			if name == "telemetry-attempt" && TouchAttempt(o.Base, o.Now().Add(48*time.Hour)) {
				t.Fatal("touched symlink")
			}
			b, _ := os.ReadFile(filepath.Join(o.Base, "victim"))
			if string(b) != "unchanged" {
				t.Fatal("victim changed")
			}
		})
	}
	o := testOptions(t)
	if err := os.MkdirAll(o.Base, 0o700); err != nil {
		t.Fatal(err)
	}
	if !FlushDue(o.Base, o.Now()) || !TouchAttempt(o.Base, o.Now()) || FlushDue(o.Base, o.Now()) ||
		TouchAttempt(o.Base, o.Now()) || FlushDue(o.Base, o.Now().Add(24*time.Hour-time.Nanosecond)) || !FlushDue(o.Base, o.Now().Add(24*time.Hour)) {
		t.Fatal("wrong marker cadence")
	}
}

// A state directory behind a symlinked parent (a dotfile manager, /home on another disk, macOS /tmp)
// must work; only a symlinked leaf is refused. An opt-out that cannot be saved is the worst outcome.
func TestSymlinkedParentWorks(t *testing.T) {
	o := testOptions(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	o.Base = filepath.Join(link, "state")
	if _, err := SaveChoice(o, false, true); err != nil {
		t.Fatalf("opt-out behind a symlinked parent: %v", err)
	}
	if d := Decide(o); !d.Saved || d.Status != "off" || d.Reason != "your choice" {
		t.Fatalf("decision: %+v", d)
	}
	if !TouchAttempt(o.Base, o.Now()) {
		t.Fatal("marker behind a symlinked parent")
	}
}

// Without a home directory the base is relative; files planted in the working directory are not state.
func TestRelativeBaseRefused(t *testing.T) {
	o := testOptions(t)
	dir := t.TempDir()
	t.Chdir(dir)
	saved(t, Options{Base: dir}, onState(true))
	put(t, filepath.Join(dir, ".caveman-cloud", "config.json"), `{"telemetry":{"enabled":false,"decidedAt":"2026-10-01T00:00:00Z","promptVersion":1}}`)
	o.Base, o.Home = "", ""
	if d := Decide(o); d.Saved || d.Sendable || d.Status != "not started" {
		t.Fatalf("planted files were read: %+v", d)
	}
	if _, err := SaveChoice(o, true, true); err == nil {
		t.Fatal("saved into the working directory")
	}
	if TouchAttempt(o.Base, o.Now()) {
		t.Fatal("touched a marker in the working directory")
	}
}

// decided_at bounds which days are sent, so only a real change of choice moves it.
func TestDecidedAtMovesOnlyOnChange(t *testing.T) {
	o := testOptions(t)
	first, err := SaveChoice(o, true, false)
	if err != nil {
		t.Fatal(err)
	}
	o.Now = func() time.Time { return time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC) }
	again, err := SaveChoice(o, true, true)
	if err != nil || again.DecidedAt != first.DecidedAt || !again.Explicit || again.AnonymousID != first.AnonymousID {
		t.Fatalf("repeated on: %+v then %+v (%v)", first, again, err)
	}
	if off, err := SaveChoice(o, false, true); err != nil || off.DecidedAt == first.DecidedAt {
		t.Fatalf("off keeps the old date: %+v %v", off, err)
	}
	o.Now = func() time.Time { return time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC) }
	if on, err := SaveChoice(o, true, true); err != nil || on.DecidedAt != "2026-10-12T12:00:00Z" || on.AnonymousID == first.AnonymousID {
		t.Fatalf("on after off: %+v %v", on, err)
	}
}

func TestInAgent(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if InAgent(get) {
		t.Fatal("empty environment is an agent")
	}
	for _, k := range []string{"CLAUDECODE", "CODEX_SANDBOX", "CURSOR_AGENT", "GEMINI_CLI", "OPENCODE", "AGENT"} {
		env = map[string]string{k: "1"}
		if !InAgent(get) {
			t.Errorf("%s not recognized", k)
		}
	}
}
