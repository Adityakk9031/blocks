package telemetry

import (
	"fmt"
	"reflect"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"
)

// Event is the entire wire contract. Strings and map keys are validated before every send.
// Keep free-form local data in stats; adding a wire field must pass the reflection guard test.
type Event struct {
	Schema         string `json:"schema"`
	Event          string `json:"event"`
	TS             string `json:"ts"`
	AnonymousID    string `json:"anonymous_id"`
	CLIVersion     string `json:"cli_version"`
	OS             string `json:"os"`
	Arch           string `json:"arch"`
	InstallChannel string `json:"install_channel"`
	Linked         bool   `json:"linked"`
	Command        string `json:"command,omitempty"`
	Subcommand     string `json:"subcommand,omitempty"`
	Block          string `json:"block,omitempty"`
	DurationMS     int    `json:"duration_ms,omitempty"`
	ExitClass      string `json:"exit_class,omitempty"`
	ErrorClass     string `json:"error_class,omitempty"`
	ScanCounts
	Day string `json:"day,omitempty"`
	DailyCounts
}

// ScanCounts contains only measured scan totals; no shapes, examples or names are retained.
type ScanCounts struct {
	ScanWindowDays     int `json:"scan_window_days,omitempty"`
	ScanCommands       int `json:"scan_commands,omitempty"`
	ScanScripts        int `json:"scan_scripts,omitempty"`
	ScanEdits          int `json:"scan_edits,omitempty"`
	ScanShapes         int `json:"scan_shapes,omitempty"`
	ScanRepeatedShapes int `json:"scan_repeated_shapes,omitempty"`
	ScanCoveredShapes  int `json:"scan_covered_shapes,omitempty"`
	ScanCoveredScripts int `json:"scan_covered_scripts,omitempty"`
	ScanTopCount       int `json:"scan_top_count,omitempty"`
}

// DailyCounts holds cross-repo counts. Map keys are restricted to public first-party names/harnesses.
type DailyCounts struct {
	ReposActive      int            `json:"repos_active,omitempty"`
	Sessions         int            `json:"sessions,omitempty"`
	Scripts          int            `json:"scripts,omitempty"`
	Hints            int            `json:"hints,omitempty"`
	HintsFollowed    int            `json:"hints_followed,omitempty"`
	PromoteHints     int            `json:"promote_hints,omitempty"`
	Calls            int            `json:"calls,omitempty"`
	Runs             int            `json:"runs,omitempty"`
	RunsFailed       int            `json:"runs_failed,omitempty"`
	RunsUser         int            `json:"runs_user,omitempty"`
	VerifyPass       int            `json:"verify_pass,omitempty"`
	VerifyFail       int            `json:"verify_fail,omitempty"`
	BytesFull        int            `json:"bytes_full,omitempty"`
	BytesReturned    int            `json:"bytes_returned,omitempty"`
	BlocksFirstParty int            `json:"blocks_first_party,omitempty"`
	BlocksUser       int            `json:"blocks_user,omitempty"`
	FirstPartyRuns   map[string]int `json:"first_party_runs,omitempty"`
	HarnessSessions  map[string]int `json:"harness_sessions,omitempty"`
}

// NewEvent builds common fields from the saved choice and injected process inputs.
func NewEvent(o Options, s State, kind string) Event {
	return Event{Schema: "blocks/v1", Event: kind, TS: o.now().Format(time.RFC3339),
		AnonymousID: s.AnonymousID, CLIVersion: o.Version, OS: runtime.GOOS, Arch: runtime.GOARCH,
		InstallChannel: s.InstallChannel, Linked: s.Linked}
}

type stringValidator func(string, []string) bool

func oneOf(values ...string) stringValidator {
	return func(v string, _ []string) bool { return slices.Contains(values, v) }
}

// Lazy regex compilation keeps all validation work off the hook's rule-1 path.
var uuidPattern = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
})
var versionPattern = sync.OnceValue(func() *regexp.Regexp {
	return regexp.MustCompile(`^[A-Za-z0-9._+-]{1,64}$`)
})

func validUUID(v string) bool { return uuidPattern().MatchString(v) }

// stringValidators is deliberately explicit: new strings fail closed until reviewed here.
var stringValidators = map[string]stringValidator{
	"Schema": oneOf("blocks/v1"),
	"Event":  oneOf("first_run", "command_run", "daily_usage"),
	"TS": func(v string, _ []string) bool {
		_, err := time.Parse(time.RFC3339, v)
		return err == nil && strings.HasSuffix(v, "Z")
	},
	"AnonymousID": func(v string, _ []string) bool { return validUUID(v) },
	"CLIVersion":  func(v string, _ []string) bool { return versionPattern().MatchString(v) },
	"OS": oneOf("aix", "android", "darwin", "dragonfly", "freebsd", "hurd", "illumos", "ios", "js",
		"linux", "netbsd", "openbsd", "plan9", "solaris", "wasip1", "windows"),
	"Arch": oneOf("386", "amd64", "amd64p32", "arm", "arm64", "arm64be", "armbe", "loong64", "mips", "mipsle",
		"mips64", "mips64le", "mips64p32", "mips64p32le", "ppc", "ppc64", "ppc64le", "riscv", "riscv64", "s390", "s390x", "sparc", "sparc64", "wasm"),
	"InstallChannel": oneOf("npx", "brew", "direct", "source"),
	"Command": oneOf("", "init", "hooks", "add", "run", "lint", "verify", "sync", "scan", "promote", "retire",
		"export", "stats", "doctor", "telemetry", "unknown"),
	"Subcommand": oneOf("", "install", "uninstall", "status", "on", "off"),
	"Block":      func(v string, firstParty []string) bool { return v == "" || slices.Contains(firstParty, v) },
	"ExitClass":  oneOf("", "ok", "error"),
	"ErrorClass": oneOf("", "usage", "failed"),
	"Day": func(v string, _ []string) bool {
		_, err := time.Parse(time.DateOnly, v)
		return v == "" || err == nil
	},
}

var mapValidators = map[string]stringValidator{
	"FirstPartyRuns":  func(v string, firstParty []string) bool { return slices.Contains(firstParty, v) },
	"HarnessSessions": oneOf("claude", "codex", "cursor", "generic"),
}

// Validate rejects fields outside the fixed schema, negative counts and mismatched event groups.
func (e Event) Validate(firstParty []string) error {
	if err := validateFields(reflect.ValueOf(e), firstParty); err != nil {
		return err
	}
	command := e.Command != "" || e.Subcommand != "" || e.Block != "" || e.DurationMS != 0 || e.ExitClass != "" || e.ErrorClass != ""
	scan := e.ScanCounts != (ScanCounts{})
	daily := e.Day != "" || !reflect.ValueOf(e.DailyCounts).IsZero()
	switch e.Event {
	case "first_run":
		if command || scan || daily {
			return fmt.Errorf("first_run has extra fields")
		}
	case "command_run":
		if daily || e.Command == "" || e.ExitClass == "" || scan && (e.Command != "scan" || e.ExitClass != "ok") ||
			e.Block != "" && e.Command != "add" && e.Command != "run" ||
			(e.ExitClass == "ok") != (e.ErrorClass == "") {
			return fmt.Errorf("invalid command_run fields")
		}
	case "daily_usage":
		if command || scan || e.Day == "" {
			return fmt.Errorf("invalid daily_usage fields")
		}
	}
	return nil
}

func validateFields(v reflect.Value, firstParty []string) error {
	for i := 0; i < v.NumField(); i++ {
		f, name := v.Field(i), v.Type().Field(i).Name
		switch f.Kind() {
		case reflect.Struct:
			if err := validateFields(f, firstParty); err != nil {
				return err
			}
		case reflect.String:
			check := stringValidators[name]
			if check == nil || !check(f.String(), firstParty) {
				return fmt.Errorf("invalid %s", name)
			}
		case reflect.Int:
			if f.Int() < 0 {
				return fmt.Errorf("negative %s", name)
			}
		case reflect.Bool:
		case reflect.Map:
			check := mapValidators[name]
			if check == nil || f.Type().Key().Kind() != reflect.String || f.Type().Elem().Kind() != reflect.Int {
				return fmt.Errorf("unreviewed map %s", name)
			}
			for it := f.MapRange(); it.Next(); {
				if !check(it.Key().String(), firstParty) || it.Value().Int() < 0 {
					return fmt.Errorf("invalid map %s", name)
				}
			}
		default:
			return fmt.Errorf("unreviewed field %s", name)
		}
	}
	return nil
}
