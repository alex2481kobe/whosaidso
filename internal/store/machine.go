package store

// The persistent machine identity lives here: one generated id per user home,
// created once and read back on every run. Git identity, the run lifecycle and
// comparability rules do not.

import (
	"crypto/rand"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// MachineIDFile is the machine identity's name in the WhoSaidSo home (Home). It holds one
// ULID and a newline, nothing else.
const MachineIDFile = "machine-id"

// MachineID returns this machine's persistent generated id, creating it on first
// use. It is GENERATED, never derived from a hostname or hardware serial: those
// are spellings a person can change or two machines can share, and an identity
// that can collide would make two machines look like one condition.
//
// The file is published by link, so it never changes once it exists: two first
// runs racing each other both read the winner's id. A file that is present but
// unreadable or malformed is an error, never replaced, because a new id would
// silently make this machine a different condition from its own earlier runs.
func MachineID() (model.ID, error) {
	home, err := Home()
	if err != nil {
		return "", err
	}
	return machineIDAt(home)
}

func machineIDAt(dir string) (model.ID, error) {
	final := filepath.Join(dir, MachineIDFile)
	if id, err := readMachineID(final); !os.IsNotExist(err) {
		return id, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", storeFault("io", dir, err.Error())
	}
	if info, err := os.Lstat(dir); err != nil || !info.IsDir() {
		return "", storeFault("io", dir, "the whosaidso home must be a real directory")
	}
	id, err := model.NewID(time.Now(), rand.Reader)
	if err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".machine-id-*.tmp")
	if err != nil {
		return "", storeFault("io", dir, err.Error())
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(string(id) + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", storeFault("io", tmp.Name(), err.Error())
	}
	if err := os.Link(tmp.Name(), final); err != nil && !os.IsExist(err) {
		return "", storeFault("io", final, err.Error())
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	// Whoever published first, the file on disk is the identity.
	return readMachineID(final)
}

// readMachineID returns an os.IsNotExist error only when no file exists.
func readMachineID(path string) (model.ID, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", err
		}
		return "", storeFault("io", path, err.Error())
	}
	if !info.Mode().IsRegular() {
		return "", storeFault("invalid-field", path, "the machine id must be a regular file, never a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", storeFault("io", path, err.Error())
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 64))
	if err != nil {
		return "", storeFault("io", path, err.Error())
	}
	text, ok := strings.CutSuffix(string(raw), "\n")
	if id := model.ID(text); ok && model.ValidID(id) {
		return id, nil
	}
	return "", storeFault("invalid-field", path, fmt.Sprintf("expected one ULID and a newline, found %q; the file is left as it is, since replacing it would change this machine's identity", raw))
}
