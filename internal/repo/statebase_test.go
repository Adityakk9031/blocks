package repo

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStateBase(t *testing.T) {
	home, xdg := t.TempDir(), t.TempDir()
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	t.Setenv(key, home)
	for _, value := range []string{"", "relative", xdg} {
		t.Setenv("XDG_STATE_HOME", value)
		want := filepath.Join(home, ".local", "state", "caveman-blocks")
		if value == xdg {
			want = filepath.Join(xdg, "caveman-blocks")
		}
		got, err := StateBase()
		if err != nil || got != want {
			t.Fatalf("StateBase with %q: %q, %v; want %q", value, got, err, want)
		}
		if _, err := os.Stat(got); !os.IsNotExist(err) {
			t.Fatal("StateBase created state")
		}
	}
}
