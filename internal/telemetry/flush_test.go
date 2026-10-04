package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func fakeCurl(t *testing.T, fail bool) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake curl is a POSIX shell fixture")
	}
	bin, record := t.TempDir(), filepath.Join(t.TempDir(), "argv")
	// Only PATH is replaced in the test process; decision controls always use injected getenv.
	t.Setenv("PATH", bin)
	exit := "0"
	if fail {
		exit = "22"
	}
	quote := "'" + strings.ReplaceAll(record, "'", "'\"'\"'") + "'"
	put(t, filepath.Join(bin, "curl"), "#!/bin/sh\nprintf '%s\\n' \"$@\" >> "+quote+"\nprintf '%s\\n' --end-- >> "+quote+"\nexit "+exit+"\n")
	if err := os.Chmod(filepath.Join(bin, "curl"), 0o700); err != nil {
		t.Fatal(err)
	}
	return record
}

func readBatches(t *testing.T, path string) ([][]Event, string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(string(b), "\n")
	var batches [][]Event
	for i, line := range lines {
		if line == "--data-binary" {
			var events []Event
			if i+1 == len(lines) || json.Unmarshal([]byte(lines[i+1]), &events) != nil {
				t.Fatalf("invalid payload argv: %s", b)
			}
			batches = append(batches, events)
		}
	}
	return batches, string(b)
}

func TestFlush(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "failure"}[fail], func(t *testing.T) {
			o := testOptions(t)
			saved(t, o, onState(true))
			usageFixture(t, o)
			payload, _ := json.Marshal([]Event{commandEvent(o)})
			o.Getenv = func(k string) string {
				return map[string]string{"CAVEMAN_BLOCKS_TELEMETRY_EVENTS": string(payload),
					"CAVEMAN_BLOCKS_TELEMETRY_URL": "https://telemetry.example.invalid/fixture"}[k]
			}
			record := fakeCurl(t, fail)
			before, _ := os.ReadFile(filepath.Join(o.Base, "telemetry.json"))
			Flush(o)
			batches, argv := readBatches(t, record)
			if len(batches) != 1 || len(batches[0]) != 3 || batches[0][0].Event != "command_run" ||
				batches[0][1].Runs != 6 || batches[0][1].RunsUser != 4 || batches[0][1].BlocksUser != 1 || batches[0][2].HintsFollowed != 2 {
				t.Fatalf("payload counts: %+v", batches)
			}
			for _, wanted := range []string{"--silent\n--show-error\n--fail\n--max-time\n10\n--request\nPOST\n", "--header\ncontent-type: application/json\n",
				"--user-agent\ncaveman-blocks/1.2.3\n", "--url\nhttps://telemetry.example.invalid/fixture\n--end--\n"} {
				if !strings.Contains(argv, wanted) {
					t.Errorf("curl argv missing %q", wanted)
				}
			}
			if !strings.HasPrefix(argv, "-q\n--globoff\n--noproxy\n127.0.0.1,localhost\n") {
				t.Errorf("-q must be curl's first argument (or ~/.curlrc is applied), then --globoff and --noproxy: %q", argv[:60])
			}
			for _, forbidden := range []string{"acme-secret-report", "secret-session", "second-secret", "wrong-secret", "private-fingerprint", "private-repo", "today-private", "future-private", "old-private", o.Base, "script text"} {
				if strings.Contains(argv, forbidden) {
					t.Errorf("privacy leak: %s", forbidden)
				}
			}
			sent, err := os.ReadFile(filepath.Join(o.Base, "telemetry-sent"))
			if fail && !os.IsNotExist(err) || !fail && (err != nil || string(sent) != "2026-10-03\n") {
				t.Fatalf("sent marker: %q %v", sent, err)
			}
			after, _ := os.ReadFile(filepath.Join(o.Base, "telemetry.json"))
			if string(after) != string(before) {
				t.Fatal("Flush rewrote choice")
			}
		})
	}
}

func TestFlushNotSendable(t *testing.T) {
	for _, choice := range []string{"absent", "off", "old-disclosure", "invalid-id", "env-off", "CI", "bad-url"} {
		t.Run(choice, func(t *testing.T) {
			o := testOptions(t)
			s := onState(true)
			if choice == "off" {
				s.Enabled = false
			}
			if choice == "old-disclosure" {
				s.Disclosure = 0
			}
			if choice == "invalid-id" {
				s.AnonymousID = "secret"
			}
			if choice != "absent" {
				saved(t, o, s)
			}
			usageFixture(t, o)
			o.Getenv = func(key string) string {
				if choice == "env-off" && key == "DO_NOT_TRACK" || choice == "CI" && key == "CI" {
					return "1"
				}
				if choice == "bad-url" && key == "CAVEMAN_BLOCKS_TELEMETRY_URL" {
					return "-K/tmp/attacker.curlrc"
				}
				return ""
			}
			record := fakeCurl(t, false)
			Flush(o)
			if _, err := os.Stat(record); !os.IsNotExist(err) {
				t.Fatal("sent without a sendable choice")
			}
			if _, err := os.Stat(filepath.Join(o.Base, "telemetry-sent")); !os.IsNotExist(err) {
				t.Fatal("advanced unsendable rollup")
			}
		})
	}
}

func TestFlushBatchesAndMissingCurl(t *testing.T) {
	o := testOptions(t)
	saved(t, o, onState(true))
	usageFixture(t, o)
	var events []Event
	for range 41 {
		events = append(events, commandEvent(o))
	}
	b, _ := json.Marshal(events)
	o.Getenv = func(k string) string {
		if k == "CAVEMAN_BLOCKS_TELEMETRY_EVENTS" {
			return string(b)
		}
		return ""
	}
	record := fakeCurl(t, false)
	Flush(o)
	batches, argv := readBatches(t, record)
	if len(batches) != 3 || len(batches[0]) != 20 || len(batches[1]) != 20 || len(batches[2]) != 3 || !strings.Contains(argv, DefaultURL) {
		t.Fatalf("batches: %+v", batches)
	}
	if err := os.Remove(filepath.Join(o.Base, "telemetry-sent")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	Flush(o)
	if _, err := os.Stat(filepath.Join(o.Base, "telemetry-sent")); !os.IsNotExist(err) {
		t.Fatal("missing curl advanced marker")
	}
}

func TestFlushEmptyScanAdvancesWithoutCurl(t *testing.T) {
	o := testOptions(t)
	saved(t, o, onState(true))
	t.Setenv("PATH", t.TempDir())
	Flush(o)
	b, err := os.ReadFile(filepath.Join(o.Base, "telemetry-sent"))
	if err != nil || string(b) != "2026-10-03\n" {
		t.Fatalf("empty complete scan: %q %v", b, err)
	}
}

func TestFlushFailedSecondDailyBatchKeepsMarker(t *testing.T) {
	o := testOptions(t)
	saved(t, o, onState(true))
	usageFixture(t, o)
	var extras []Event
	for range 19 {
		extras = append(extras, commandEvent(o))
	}
	b, _ := json.Marshal(extras)
	o.Getenv = func(k string) string {
		if k == "CAVEMAN_BLOCKS_TELEMETRY_EVENTS" {
			return string(b)
		}
		return ""
	}
	record := fakeCurl(t, false)
	curl := filepath.Join(os.Getenv("PATH"), "curl")
	script, err := os.ReadFile(curl)
	if err != nil {
		t.Fatal(err)
	}
	put(t, curl, strings.Replace(string(script), "exit 0", "case \"$*\" in *2026-10-02*) exit 22 ;; esac\nexit 0", 1))
	Flush(o)
	batches, _ := readBatches(t, record)
	if len(batches) != 2 || len(batches[0]) != 20 || len(batches[1]) != 1 {
		t.Fatalf("daily batches: %+v", batches)
	}
	if _, err := os.Stat(filepath.Join(o.Base, "telemetry-sent")); !os.IsNotExist(err) {
		t.Fatal("partially delivered daily rollup advanced marker")
	}
}

func TestFlushOffDuringSendIsNotClobbered(t *testing.T) {
	o := testOptions(t)
	saved(t, o, onState(true))
	usageFixture(t, o)
	var extras []Event
	for range 21 {
		extras = append(extras, commandEvent(o))
	}
	b, _ := json.Marshal(extras)
	o.Getenv = func(k string) string {
		if k == "CAVEMAN_BLOCKS_TELEMETRY_EVENTS" {
			return string(b)
		}
		return ""
	}
	record := fakeCurl(t, false)
	curl := filepath.Join(os.Getenv("PATH"), "curl")
	script, err := os.ReadFile(curl)
	if err != nil {
		t.Fatal(err)
	}
	path := "'" + strings.ReplaceAll(filepath.Join(o.Base, "telemetry.json"), "'", "'\"'\"'") + "'"
	put(t, curl, strings.Replace(string(script), "exit 0", `printf '%s\n' '{"enabled":false,"explicit":true}' > `+path+"\nexit 0", 1))
	Flush(o)
	batches, _ := readBatches(t, record)
	if len(batches) != 1 || Decide(o).Status != "off" || Decide(o).State.AnonymousID != "" {
		t.Fatal("off overwritten or sending continued after off")
	}
	if _, err := os.Stat(filepath.Join(o.Base, "telemetry-sent")); !os.IsNotExist(err) {
		t.Fatal("unsent daily batch advanced marker")
	}
}
