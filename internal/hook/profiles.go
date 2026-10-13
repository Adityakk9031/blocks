package hook

import (
	_ "embed"
	"fmt"
	"os"
	"regexp"
	"sort"

	"github.com/pelletier/go-toml/v2"
)

//go:embed profiles.toml
var profilesTOML []byte

// HookEvent names a harness event and its tool matcher.
type HookEvent struct {
	Name    string `toml:"name"`
	Matcher string `toml:"matcher"`
}

// Timeout is a hook timeout in the harness's own unit ("s" or "ms").
type Timeout struct {
	Value int    `toml:"value"`
	Unit  string `toml:"unit"`
}

// Profile is one harness table of profiles.toml (docs/INTEGRATION.md, layer 3). install reads the
// dialect, events and timeout from it.
type Profile struct {
	Name         string    `toml:"-"`
	Phase        int       `toml:"phase"`
	Dialect      string    `toml:"dialect"` // the --harness value written into the hook command
	Detect       []string  `toml:"detect"`  // env-resolved, like Config
	Config       string    `toml:"config"`  // a leading ${VAR:-default} already resolved; may start with ~/
	ConfigFormat string    `toml:"config_format"`
	PreEvent     HookEvent `toml:"pre_event"`
	PostEvent    HookEvent `toml:"post_event"` // zero when the pre-run hint is enough
	Timeout      Timeout   `toml:"timeout"`
	TrustNote    string    `toml:"trust_note"`
}

var envDefault = regexp.MustCompile(`^\$\{([A-Z_]+):-([^}]*)\}`)

// resolveEnv replaces a leading ${VAR:-default} with $VAR when it is set and non-empty, else default.
func resolveEnv(p string) string {
	m := envDefault.FindStringSubmatch(p)
	if m == nil {
		return p
	}
	if v := os.Getenv(m[1]); v != "" {
		return v + p[len(m[0]):]
	}
	return m[2] + p[len(m[0]):]
}

// Profiles returns the embedded harness profiles sorted by phase, then name, their config and detect
// paths resolved against the environment (CLAUDE_CONFIG_DIR, CODEX_HOME).
func Profiles() ([]Profile, error) {
	var m map[string]Profile
	if err := toml.Unmarshal(profilesTOML, &m); err != nil {
		return nil, fmt.Errorf("profiles.toml: %w", err)
	}
	out := make([]Profile, 0, len(m))
	for name, p := range m {
		p.Name = name
		p.Config = resolveEnv(p.Config)
		for i, d := range p.Detect {
			p.Detect[i] = resolveEnv(d)
		}
		if p.Phase == 0 {
			p.Phase = 1
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Phase != out[j].Phase {
			return out[i].Phase < out[j].Phase
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}
