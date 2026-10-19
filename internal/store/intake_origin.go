package store

// Which checkout captured an intake packet. Intake is shared by every checkout
// of a project on one machine, while each checkout admits into its own ledger,
// so a checkout's todo must tell its own pending packets from those captured
// in a sibling worktree, which that worktree will review. The origin is a
// local note in the home, beside intake and never inside a packet: it is in no
// digest, never in a ledger, and a packet without one (captured before origins
// were noted) reads as it always did.

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// originDir holds one file per packet, named by command id, holding the
// canonical root of the checkout that captured it and a newline.
func originDir(project Project) (string, error) {
	encoded, err := encodeProjectID(project.ID)
	if err != nil {
		return "", err
	}
	home, err := Home()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "intake-origin", encoded), nil
}

// canonicalRoot is the checkout's root with symlinks resolved, so two
// spellings of one checkout name one origin.
func canonicalRoot(project Project) (string, error) {
	root, err := filepath.EvalSymlinks(project.Root)
	if err != nil {
		return "", storeFault("io", project.Root, err.Error())
	}
	return root, nil
}

// noteIntakeOrigin records the capturing checkout before the packet is
// published, so a visible packet always has its origin. An origin already
// noted for the id is kept: a retry from elsewhere does not move it.
func noteIntakeOrigin(project Project, id model.ID) error {
	root, err := canonicalRoot(project)
	if err != nil {
		return err
	}
	dir, err := originDir(project)
	if err != nil {
		return err
	}
	for _, d := range []string{filepath.Dir(dir), dir} {
		if err := intakeMkdir(d); err != nil {
			return err
		}
	}
	final := filepath.Join(dir, string(id))
	if _, err := os.Lstat(final); err == nil {
		return nil
	}
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return storeFault("io", dir, err.Error())
	}
	tmp := filepath.Join(dir, string(id)+"-"+hex.EncodeToString(suffix)+".tmp")
	if err := os.WriteFile(tmp, []byte(root+"\n"), 0600); err != nil {
		return storeFault("io", tmp, err.Error())
	}
	defer os.Remove(tmp)
	// Link, not rename: link never replaces an origin another capture noted
	// first for the same id.
	if err := os.Link(tmp, final); err != nil && !os.IsExist(err) {
		return storeFault("io", final, err.Error())
	}
	return nil
}

// IntakeOrigin is the canonical root of the checkout that captured packet id,
// and whether one was noted.
func IntakeOrigin(project Project, id model.ID) (string, bool, error) {
	dir, err := originDir(project)
	if err != nil {
		return "", false, err
	}
	path := filepath.Join(dir, string(id))
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return "", false, nil
	}
	if err := checkIntakePath(path, false); err != nil {
		return "", false, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", false, storeFault("io", path, err.Error())
	}
	root, ok := strings.CutSuffix(string(raw), "\n")
	if !ok || !filepath.IsAbs(root) || filepath.Clean(root) != root || strings.ContainsAny(root, "\x00\n") {
		return "", false, storeFault("intake-corrupt", path, fmt.Sprintf("expected one absolute clean path and a newline, found %q", raw))
	}
	return root, true, nil
}

// CapturedHere reports whether origin names this project's checkout.
func CapturedHere(project Project, origin string) (bool, error) {
	root, err := canonicalRoot(project)
	if err != nil {
		return false, err
	}
	return origin == root, nil
}

// OriginGone reports whether no checkout is left at origin: the checkout was
// moved or removed. Its packets then belong to no sibling, and read as a
// packet with no noted origin does, pending in any checkout of the project.
func OriginGone(origin string) (bool, error) {
	_, err := os.Stat(origin)
	if os.IsNotExist(err) {
		return true, nil
	}
	if err != nil {
		return false, storeFault("io", origin, err.Error())
	}
	return false, nil
}
