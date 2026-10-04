// Package telemetry sends the fixed usage contract in docs/TELEMETRY.md through the system curl.
// Environment reads are injected; no network library is linked into the hook binary.
package telemetry

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/caveman-ai/blocks/internal/repo"
)

// DisclosureVersion identifies the disclosure required before any event can be sent.
const DisclosureVersion = 1

// Disclosure is the documented, verbatim first-interactive-command notice.
const Disclosure = "usage counts on · commands, hints shown and followed, block runs, a random install ID, OS and your IP address · never scripts, file paths, repo names or names of blocks you wrote · this is what keeps Blocks free · off: caveman-blocks telemetry off · https://github.com/caveman-ai/blocks/blob/main/docs/TELEMETRY.md"

// DefaultURL is the documented receiver; only CAVEMAN_BLOCKS_TELEMETRY_URL overrides it.
const DefaultURL = "https://xvfgtprkhzlvegvmeefq.supabase.co/functions/v1/blocks-telemetry"

// Options supplies process inputs. Tests provide paths, a clock and getenv without real env reads.
type Options struct {
	Base, Home, Executable, Version string
	FirstParty                      []string
	Getenv                          func(string) string
	Now                             func() time.Time
}

// System resolves process inputs without reading or writing telemetry state.
func System(version string, firstParty []string) Options {
	base, _ := repo.StateBase()
	home, _ := os.UserHomeDir()
	exe, _ := os.Executable()
	return Options{Base: base, Home: home, Executable: exe, Version: version,
		FirstParty: firstParty, Getenv: os.Getenv, Now: time.Now}
}

func (o Options) env(key string) string {
	if o.Getenv == nil {
		return ""
	}
	return o.Getenv(key)
}

func (o Options) now() time.Time {
	if o.Now == nil {
		return time.Now().UTC()
	}
	return o.Now().UTC()
}

// Endpoint returns the documented destination, or a valid CAVEMAN_BLOCKS_TELEMETRY_URL. An override
// that is not https (or plain http to this machine, for tests) yields "": nothing is sent, rather
// than sent somewhere the override did not name or handed to curl as an option.
func (o Options) Endpoint() string {
	raw := o.env("CAVEMAN_BLOCKS_TELEMETRY_URL")
	if raw == "" {
		return DefaultURL
	}
	// No whitespace, and none of curl's URL glob characters (curl also runs with --globoff).
	if strings.ContainsAny(raw, " \t\r\n{}[]") {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return "" // user:password@ can hide the real host behind a harmless-looking prefix
	}
	local := u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"
	if u.Scheme == "https" || u.Scheme == "http" && local {
		return raw
	}
	return ""
}

// State is the local telemetry.json choice, separate from the wire Event.
type State struct {
	Enabled        bool   `json:"enabled"`
	Explicit       bool   `json:"explicit"`
	DecidedAt      string `json:"decided_at"`
	Disclosure     int    `json:"disclosure"`
	AnonymousID    string `json:"anonymous_id,omitempty"`
	Linked         bool   `json:"linked"`
	InstallChannel string `json:"install_channel"`
}

// Decision reports the effective control and whether the saved disclosure and ID permit sending.
type Decision struct {
	Status, Reason string
	State          State
	Saved, EnvOff  bool
	Sendable       bool
}

// EnvOff names the environment switch that has telemetry off for this process, or "".
func EnvOff(getenv func(string) string) string {
	if v := getenv("DO_NOT_TRACK"); v != "" && v != "0" {
		return "DO_NOT_TRACK"
	}
	if switchOff(getenv("CAVEMAN_BLOCKS_TELEMETRY")) {
		return "CAVEMAN_BLOCKS_TELEMETRY"
	}
	if switchOff(getenv("CAVEMAN_TELEMETRY")) {
		return "CAVEMAN_TELEMETRY"
	}
	if v := getenv("CI"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return "CI"
	}
	return ""
}

// Decide applies the documented precedence without saving anything.
func Decide(o Options) Decision {
	s, saved, damaged := loadState(o.Base)
	d := Decision{State: s, Saved: saved, Status: "off"}
	if name := EnvOff(o.env); name != "" {
		d.Reason, d.EnvOff = name, name != "CI" // CI is never saved as a choice
	} else if damaged {
		d.Reason = "an unreadable telemetry.json (run caveman-blocks telemetry on or off)"
	} else if saved && s.Explicit {
		d.Reason = "your choice"
		if s.Enabled {
			d.Status = "on"
		}
	} else if c := cavemanChoice(o.Home); c.Enabled != nil && !*c.Enabled {
		d.Reason = "the Caveman CLI opt-out"
	} else if saved {
		d.Reason = "the default"
		if s.Enabled {
			d.Status = "on"
		}
	} else {
		d.Status, d.Reason = "not started", "the default (awaiting an interactive command)"
	}
	// A shared ID is ours to use only while the Caveman CLI still holds it with telemetry on.
	if d.Status == "on" && s.Linked && !linkHolds(o.Home, s) {
		d.Status, d.Reason = "not started", "the Caveman CLI retired the shared install ID (awaiting an interactive command)"
	}
	d.Sendable = d.Status == "on" && saved && s.Enabled && validUUID(s.AnonymousID) && s.Disclosure >= DisclosureVersion
	return d
}

// Observe decides and, when a switch or a choice has telemetry off, marks today off (NoteOff). Every
// process that could count something calls it, so a day on which telemetry was seen off is never
// sent, whatever its counts hold. CI is the exception: it keeps this process from sending and from
// enrolling, but it is not the person's choice (some agent harnesses export it), so it marks nothing.
// It returns the decision.
func Observe(o Options) Decision {
	d := Decide(o)
	if !d.Sendable && d.Reason != "CI" {
		NoteOff(o.Base, o.now())
	}
	return d
}

func switchOff(v string) bool {
	if v == "" {
		return false
	}
	v = strings.ToLower(strings.TrimSpace(v))
	return v != "1" && v != "true" && v != "on"
}

// loadState reads telemetry.json. damaged means something is there that cannot be read as a choice:
// it is never mistaken for "nothing decided", so the default is never written over it.
func loadState(base string) (s State, saved, damaged bool) {
	var file struct {
		State
		Enabled *bool `json:"enabled"`
	}
	b, err := readSafe(base, "telemetry.json")
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, false, false
	}
	if err != nil || json.Unmarshal(b, &file) != nil || file.Enabled == nil {
		return State{}, false, true
	}
	file.State.Enabled = *file.Enabled
	return file.State, true, false
}

// linkHolds reports whether the Caveman CLI still has telemetry on under the ID we share with it.
func linkHolds(home string, s State) bool {
	c := cavemanChoice(home)
	return c.Enabled != nil && *c.Enabled && strings.EqualFold(c.AnonymousID, s.AnonymousID)
}

type cavemanTelemetry struct {
	Enabled       *bool  `json:"enabled"`
	DecidedAt     string `json:"decidedAt"`
	PromptVersion int    `json:"promptVersion"`
	AnonymousID   string `json:"anonymousId"`
}

func cavemanChoice(home string) cavemanTelemetry {
	var c struct {
		Telemetry cavemanTelemetry `json:"telemetry"`
	}
	// The Caveman CLI's own file, not our state: that CLI supports a symlinked config, so follow it.
	b, err := readUser(home, ".caveman-cloud/config.json")
	if err != nil || json.Unmarshal(b, &c) != nil || c.Telemetry.Enabled == nil || c.Telemetry.DecidedAt == "" {
		return cavemanTelemetry{}
	}
	return c.Telemetry
}

// SaveChoice atomically saves an explicit on/off or the disclosed default. Off always removes the
// ID and link. An existing on ID and channel survive repeated on commands.
func SaveChoice(o Options, enabled, explicit bool) (State, error) {
	s, _, _ := loadState(o.Base)
	if s.Linked && !linkHolds(o.Home, s) {
		s.AnonymousID, s.Linked = "", false // the Caveman CLI retired that ID; never keep using it
	}
	// decided_at is when telemetry last started; rollup sends nothing from before it. It stays put
	// only when telemetry was already on (a repeated `on`): off for any reason, it restarts now.
	if !(enabled && validUUID(s.AnonymousID) && Decide(o).Sendable) {
		s.DecidedAt = o.now().Format(time.RFC3339)
	}
	s.Enabled, s.Explicit = enabled, explicit
	if !enabled {
		s.AnonymousID, s.Linked = "", false
	} else {
		s.Disclosure = DisclosureVersion
		if !validUUID(s.AnonymousID) {
			c := cavemanChoice(o.Home)
			s.Linked = c.Enabled != nil && *c.Enabled && validUUID(c.AnonymousID)
			if s.Linked {
				s.AnonymousID = strings.ToLower(c.AnonymousID)
			} else {
				var b [16]byte
				if _, err := rand.Read(b[:]); err != nil {
					return State{}, err
				}
				b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
				s.AnonymousID = fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
			}
			s.InstallChannel = installChannel(o.Executable, o.Version)
		}
	}
	b, err := json.Marshal(s)
	if err != nil {
		return State{}, err
	}
	if err := writeSafe(o.Base, "telemetry.json", append(b, '\n')); err != nil {
		return State{}, err
	}
	return s, nil
}

func installChannel(exe, version string) string {
	if repo.IsCachePath(exe) {
		return "npx"
	}
	for _, part := range []string{"/Caskroom/", "/Cellar/", "/homebrew/", "/linuxbrew/"} {
		if strings.Contains(filepath.ToSlash(exe), part) {
			return "brew"
		}
	}
	if version == "0.0.0-dev" {
		return "source"
	}
	return "direct"
}

// OffNotice includes the former ID once, after a successful save removed it.
func OffNotice(id string) string {
	return "telemetry off · old install ID " + id + " · to delete what it sent: https://github.com/caveman-ai/blocks/blob/main/docs/TELEMETRY.md#delete-what-was-sent"
}
