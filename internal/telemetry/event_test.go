package telemetry

import (
	"encoding"
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

// TestWireFieldGuard requires a named validator for every string and every map key, recursively.
// Interfaces, pointers, slices and other free-form fields are rejected even if currently unused.
func TestWireFieldGuard(t *testing.T) {
	// A custom marshaler can emit any text whatever the Go type looks like, so none is allowed.
	marshalers := []reflect.Type{reflect.TypeFor[json.Marshaler](), reflect.TypeFor[encoding.TextMarshaler]()}
	plain := func(name string, typ reflect.Type) {
		for _, m := range marshalers {
			if typ.Implements(m) || reflect.PointerTo(typ).Implements(m) {
				t.Errorf("%s (%s) has a custom marshaler", name, typ)
			}
		}
	}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		plain(typ.Name(), typ)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			plain(f.Name, f.Type)
			if f.Type.Kind() == reflect.Map {
				plain(f.Name+" key", f.Type.Key())
				plain(f.Name+" value", f.Type.Elem())
			}
			switch f.Type.Kind() {
			case reflect.Struct:
				walk(f.Type)
			case reflect.Int, reflect.Bool:
			case reflect.String:
				if stringValidators[f.Name] == nil {
					t.Errorf("%s has no explicit string validator", f.Name)
				}
			case reflect.Map:
				if f.Type.Key().Kind() != reflect.String || f.Type.Elem().Kind() != reflect.Int || mapValidators[f.Name] == nil {
					t.Errorf("%s is not an allowlisted string-to-int map", f.Name)
				}
			default:
				t.Errorf("%s has free-form type %s", f.Name, f.Type)
			}
		}
	}
	walk(reflect.TypeFor[Event]())
}

func commandEvent(o Options) Event {
	e := NewEvent(o, onState(true), "command_run")
	e.Command, e.ExitClass = "stats", "ok"
	return e
}

func TestValidate(t *testing.T) {
	o := testOptions(t)
	for _, tc := range []struct {
		name string
		edit func(*Event)
		ok   bool
	}{
		{"command", func(*Event) {}, true},
		{"schema", func(e *Event) { e.Schema = "blocks/v2" }, false},
		{"event", func(e *Event) { e.Event = "script" }, false},
		{"ts UTC", func(e *Event) { e.TS = "2026-10-03T12:00:00+01:00" }, false},
		{"ts bad", func(e *Event) { e.TS = "not time" }, false},
		{"id", func(e *Event) { e.AnonymousID = "session-secret" }, false},
		{"version path", func(e *Event) { e.CLIVersion = "/secret/path" }, false},
		{"version long", func(e *Event) { e.CLIVersion = strings.Repeat("x", 65) }, false},
		{"version semver", func(e *Event) { e.CLIVersion = "v1.2.3-dev+abc" }, true},
		{"os", func(e *Event) { e.OS = "host-secret" }, false},
		{"arch", func(e *Event) { e.Arch = "custom-secret" }, false},
		{"channel", func(e *Event) { e.InstallChannel = "my-path" }, false},
		{"command secret", func(e *Event) { e.Command = "acme-secret-report" }, false},
		{"unknown", func(e *Event) { e.Command = "unknown" }, true},
		{"subcommand", func(e *Event) { e.Subcommand = "flush" }, false},
		{"public block", func(e *Event) { e.Command, e.Block = "run", "json-peek" }, true},
		{"user block", func(e *Event) { e.Command, e.Block = "run", "acme-secret-report" }, false},
		{"wrong block command", func(e *Event) { e.Block = "json-peek" }, false},
		{"exit", func(e *Event) { e.ExitClass = "timeout" }, false},
		{"error", func(e *Event) { e.ExitClass, e.ErrorClass = "error", "usage" }, true},
		{"error secret", func(e *Event) { e.ExitClass, e.ErrorClass = "error", "private failure" }, false},
		{"inconsistent exit", func(e *Event) { e.ErrorClass = "failed" }, false},
		{"negative", func(e *Event) { e.DurationMS = -1 }, false},
		{"scan", func(e *Event) { e.Command, e.ScanShapes = "scan", 2 }, true},
		{"scan wrong command", func(e *Event) { e.ScanShapes = 2 }, false},
		{"scan failed", func(e *Event) { e.Command, e.ScanShapes, e.ExitClass, e.ErrorClass = "scan", 2, "error", "failed" }, false},
		{"first run", func(e *Event) { *e = NewEvent(o, onState(true), "first_run") }, true},
		{"first extra", func(e *Event) { e.Event = "first_run" }, false},
		{"daily", func(e *Event) { *e = NewEvent(o, onState(true), "daily_usage"); e.Day = "2026-10-03" }, true},
		{"day bad", func(e *Event) { *e = NewEvent(o, onState(true), "daily_usage"); e.Day = "2026-02-30" }, false},
		{"map private", func(e *Event) { e.FirstPartyRuns = map[string]int{"acme-secret-report": 1} }, false},
		{"harness private", func(e *Event) { e.HarnessSessions = map[string]int{"secret-session": 1} }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := commandEvent(o)
			tc.edit(&e)
			if err := e.Validate(o.FirstParty); (err == nil) != tc.ok {
				t.Fatalf("Validate: %v, want valid=%v", err, tc.ok)
			}
		})
	}
	for _, kind := range []string{"first_run", "command_run", "daily_usage"} {
		e := NewEvent(o, onState(true), kind)
		if kind == "command_run" {
			e = commandEvent(o)
		}
		if kind == "daily_usage" {
			e.Day = "2026-10-03"
		}
		b, _ := json.Marshal(e)
		for _, absent := range []string{"scan_shapes", "first_party_runs", "harness_sessions", "runs_failed"} {
			if strings.Contains(string(b), absent) {
				t.Errorf("absent group serialized: %s", b)
			}
		}
	}
}

func TestExtraEventsStrict(t *testing.T) {
	o := testOptions(t)
	b, _ := json.Marshal(commandEvent(o))
	good := string(b)
	bad := strings.TrimSuffix(good, "}") + `,"script":"private"}`
	if got := extraEvents("["+good+","+bad+"]", o, onState(true)); len(got) != 1 {
		t.Fatalf("strict individual events: %+v", got)
	}
	for _, raw := range []string{"[" + good + "] trailing", good, "null", "{}", "[null]"} {
		if len(extraEvents(raw, o, onState(true))) != 0 {
			t.Errorf("accepted malformed array %s", raw)
		}
	}
}

// Whatever the handed-over event claims about who sent it, it leaves under this install's identity.
func TestExtraEventsRebuildIdentity(t *testing.T) {
	o := testOptions(t)
	forged := commandEvent(o)
	forged.AnonymousID, forged.CLIVersion, forged.OS, forged.Arch, forged.InstallChannel, forged.Linked =
		"deadbeef-dead-4ead-8ead-deadbeefdead", "acme-internal.customer-x", "plan9", "mips", "brew", true
	b, _ := json.Marshal([]Event{forged})
	got := extraEvents(string(b), o, onState(true))
	want := NewEvent(o, onState(true), "command_run")
	if len(got) != 1 || got[0].AnonymousID != testID || got[0].CLIVersion != "1.2.3" || got[0].OS != want.OS ||
		got[0].Arch != want.Arch || got[0].InstallChannel != "direct" || got[0].Linked || got[0].Command != "stats" {
		t.Fatalf("identity not rebuilt: %+v", got)
	}
}

func TestDevNullIsNotTerminal(t *testing.T) {
	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if isTerminal(f.Fd()) {
		t.Fatal("/dev/null accepted as consent")
	}
}
