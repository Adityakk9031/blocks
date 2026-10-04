package telemetry

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/caveman-ai/blocks/internal/repo"
)

// path joins name onto base. A relative base (no home directory) is refused, so a file planted in
// the working directory can never be read as, or written over, telemetry state.
func path(base, name string) (string, error) {
	if !filepath.IsAbs(base) {
		return "", fmt.Errorf("telemetry: no absolute state directory")
	}
	return filepath.Join(base, name), nil
}

// readSafe reads base/name when it is a regular file, never through a symlinked leaf.
func readSafe(base, name string) ([]byte, error) {
	p, err := path(base, name)
	if err != nil {
		return nil, err
	}
	fi, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("telemetry: %s is not a regular file", name)
	}
	f, err := os.OpenFile(p, os.O_RDONLY|repo.NoFollow, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	// ponytail: a control file over 1 MiB is ignored; none of ours comes near it.
	if fi.Size() > 1<<20 {
		return nil, fmt.Errorf("telemetry: %s is over 1 MiB", name)
	}
	return io.ReadAll(io.LimitReader(f, 1<<20))
}

// readUser reads a file the user owns, following symlinks, when it is a regular file of at most 1 MiB.
func readUser(base, name string) ([]byte, error) {
	p, err := path(base, name)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() || fi.Size() > 1<<20 {
		return nil, fmt.Errorf("telemetry: %s is not a small regular file", name)
	}
	return os.ReadFile(p)
}

// writeSafe replaces base/name atomically (temp file and rename), never through a symlinked leaf.
func writeSafe(base, name string, data []byte) error {
	if _, err := path(base, name); err != nil {
		return err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return err
	}
	return repo.WriteFileSafe(base, name, data, 0o600)
}

// FlushDue checks only the marker's mtime, including when telemetry is off. A marker from the
// future (the clock was ahead once) is due, so it cannot stall the daily attempt.
func FlushDue(base string, now time.Time) bool {
	fi, err := os.Lstat(filepath.Join(base, "telemetry-attempt"))
	if err != nil {
		return true
	}
	age := now.Sub(fi.ModTime())
	return age < 0 || age >= 24*time.Hour
}

// TouchAttempt marks a due attempt before any decision read or spawn; false means skip the attempt.
// ponytail: like PruneDue, the second check narrows but does not lock out simultaneous processes;
// the receiver stores one row per install and day, so a duplicate attempt costs one request.
func TouchAttempt(base string, now time.Time) bool {
	return FlushDue(base, now) && touch(base, "telemetry-attempt", now)
}

// NoteOff records that telemetry was seen off today (Observe). rollup never sends that day or an
// earlier one, so usage counted while it was off is not sent once it is on again. It costs one lstat
// when today is already noted.
func NoteOff(base string, now time.Time) {
	p, err := path(base, "telemetry-off")
	if err != nil {
		return
	}
	if fi, err := os.Lstat(p); err == nil && !fi.ModTime().Before(now.UTC().Truncate(24*time.Hour)) {
		return
	}
	touch(base, "telemetry-off", now)
}

// touch creates base/name if needed, never through a symlinked leaf, and sets its mtime to now.
func touch(base, name string, now time.Time) bool {
	p, err := path(base, name)
	if err != nil {
		return false
	}
	f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|repo.NoFollow, 0o600)
	if err != nil {
		return false
	}
	f.Close()
	return os.Chtimes(p, now, now) == nil
}
