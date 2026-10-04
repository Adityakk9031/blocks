// Package telemetry sends the fixed usage contract in docs/TELEMETRY.md through the system curl.
// Environment reads are injected; no network library is linked into the hook binary.
package telemetry

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
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

// Endpoint returns the override or the documented destination.
func (o Options) Endpoint() string {
	if url := o.env("CAVEMAN_BLOCKS_TELEMETRY_URL"); url != "" {
		return url
	}
	return DefaultURL
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

// Decide applies the documented precedence without saving anything.
func Decide(o Options) Decision {
	s, saved := loadState(o.Base)
	d := Decision{State: s, Saved: saved, Status: "off"}
	if v := o.env("DO_NOT_TRACK"); v != "" && v != "0" {
		d.Reason, d.EnvOff = "DO_NOT_TRACK", true
	} else if switchOff(o.env("CAVEMAN_BLOCKS_TELEMETRY")) {
		d.Reason, d.EnvOff = "CAVEMAN_BLOCKS_TELEMETRY", true
	} else if switchOff(o.env("CAVEMAN_TELEMETRY")) {
		d.Reason, d.EnvOff = "CAVEMAN_TELEMETRY", true
	} else if v := o.env("CI"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		d.Reason = "CI"
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
	d.Sendable = d.Status == "on" && saved && s.Enabled && validUUID(s.AnonymousID) && s.Disclosure >= DisclosureVersion
	return d
}

func switchOff(v string) bool {
	if v == "" {
		return false
	}
	v = strings.ToLower(strings.TrimSpace(v))
	return v != "1" && v != "true" && v != "on"
}

func loadState(base string) (State, bool) {
	var saved struct {
		State
		Enabled *bool `json:"enabled"`
	}
	b, err := readSafe(base, "telemetry.json")
	if err != nil || json.Unmarshal(b, &saved) != nil || saved.Enabled == nil {
		return State{}, false
	}
	saved.State.Enabled = *saved.Enabled
	return saved.State, true
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
	b, err := readSafe(home, ".caveman-cloud/config.json")
	if err != nil || json.Unmarshal(b, &c) != nil || c.Telemetry.Enabled == nil || c.Telemetry.DecidedAt == "" {
		return cavemanTelemetry{}
	}
	return c.Telemetry
}

// SaveChoice atomically saves an explicit on/off or the disclosed default. Off always removes the
// ID and link. An existing on ID and channel survive repeated on commands.
func SaveChoice(o Options, enabled, explicit bool) (State, error) {
	s, _ := loadState(o.Base)
	// decided_at bounds which days are sent (rollup), so a repeated `on` must not move it.
	if !(enabled && s.Enabled && validUUID(s.AnonymousID)) {
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
