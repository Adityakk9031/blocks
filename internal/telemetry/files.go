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

// FlushDue checks only the marker's mtime, including when telemetry is off.
func FlushDue(base string, now time.Time) bool {
	fi, err := os.Lstat(filepath.Join(base, "telemetry-attempt"))
	return err != nil || now.Sub(fi.ModTime()) >= 24*time.Hour
}

// TouchAttempt marks a due attempt before any decision read or spawn; false means skip the attempt.
// ponytail: like PruneDue, the second check narrows but does not lock out simultaneous processes;
// the receiver stores one row per install and day, so a duplicate attempt costs one request.
func TouchAttempt(base string, now time.Time) bool {
	if !FlushDue(base, now) {
		return false
	}
	p, err := path(base, "telemetry-attempt")
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
