//go:build windows

package store

// The Windows filesystem rules in fsys_windows.go and acl_windows.go, each
// checked against the operating system rather than against the code itself:
// rename never replaces, waits out a sharing violation for a bounded time,
// directories can be flushed, and a private directory's list is set at
// creation (read independently through PowerShell).

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRenameNeverReplacesAnExistingName(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "new"), filepath.Join(dir, "admitted")
	putFile(t, from, []byte("second"))
	// Control: os.Rename, the call this replaces, does overwrite here.
	putFile(t, to, []byte("first"))
	putFile(t, filepath.Join(dir, "probe"), []byte("probe"))
	if err := os.Rename(filepath.Join(dir, "probe"), to); err != nil {
		t.Fatalf("control: os.Rename over an existing file: %v", err)
	}
	putFile(t, to, []byte("first"))
	err := renameNoReplace(from, to)
	if !os.IsExist(err) {
		t.Fatalf("renaming onto an existing name must be refused as existing, got %v", err)
	}
	if data, _ := os.ReadFile(to); string(data) != "first" {
		t.Fatalf("the existing file was replaced: %q", data)
	}
	if data, _ := os.ReadFile(from); string(data) != "second" {
		t.Fatalf("the refused source changed: %q", data)
	}
}

func TestRenameWaitsOutASharingViolation(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "new"), filepath.Join(dir, "published")
	putFile(t, from, []byte("bytes"))
	// os.Open shares read and write but not delete, which is what a scanner's
	// brief open looks like to a rename.
	held, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	if !transientSharing(errors.Unwrap(os.Rename(from, to))) {
		t.Fatal("control: a rename of a file held open must fail with a sharing violation")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		held.Close()
	}()
	if err := renameNoReplace(from, to); err != nil {
		t.Fatalf("a handle released within the bound must not fail the rename: %v", err)
	}
	if data, _ := os.ReadFile(to); string(data) != "bytes" {
		t.Fatalf("published bytes: %q", data)
	}
}

func TestRenameGivesUpOnAHandleHeldForGood(t *testing.T) {
	dir := t.TempDir()
	from, to := filepath.Join(dir, "new"), filepath.Join(dir, "published")
	putFile(t, from, []byte("bytes"))
	held, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	var bound time.Duration
	for _, d := range sharingRetries {
		bound += d
	}
	start := time.Now()
	err = renameNoReplace(from, to)
	elapsed := time.Since(start)
	if err == nil || !transientSharing(errors.Unwrap(err)) {
		t.Fatalf("a handle that is never released must surface the sharing violation, got %v", err)
	}
	if elapsed < bound || elapsed > bound+5*time.Second {
		t.Fatalf("waited %v, want about %v", elapsed, bound)
	}
}

func TestDirectoriesAreFlushable(t *testing.T) {
	dir := t.TempDir()
	// Control: the handle os.Open gives cannot be flushed, which is why
	// openForSync exists.
	if f, err := os.Open(dir); err != nil {
		t.Fatal(err)
	} else {
		syncErr := f.Sync()
		f.Close()
		if syncErr == nil {
			t.Fatal("control: os.Open of a directory unexpectedly flushes")
		}
	}
	f, err := openForSync(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Sync(); err != nil {
		t.Fatalf("a directory opened for flushing must flush: %v", err)
	}
}

// sddl reads a path's access list through PowerShell, an instrument that
// shares no code with acl_windows.go.
func sddl(t *testing.T, path string) string {
	t.Helper()
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"(Get-Item -LiteralPath $env:WHOSAIDSO_ACL_PATH).GetAccessControl().Sddl").CombinedOutput()
	if err != nil {
		t.Fatalf("Get-Acl: %v: %s", err, out)
	}
	return strings.TrimSpace(string(out))
}

// rendered is list as sddl displays it: the same .NET renderer turns a
// well-known SID into its alias (the built-in Administrator's into LA), so an
// expectation built from SIDs is compared in that form.
func rendered(t *testing.T, list string) string {
	t.Helper()
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"([Security.AccessControl.RawSecurityDescriptor]::new($env:WHOSAIDSO_SDDL)).GetSddlForm('Access')")
	cmd.Env = append(os.Environ(), "WHOSAIDSO_SDDL="+list)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("render %s: %v: %s", list, err, out)
	}
	return strings.TrimSpace(string(out))
}

func TestPrivateDirectoryListIsSetAtCreation(t *testing.T) {
	user, err := currentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	plain, private := filepath.Join(parent, "plain"), filepath.Join(parent, "private")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHOSAIDSO_ACL_PATH", plain)
	if got := sddl(t, plain); !strings.Contains(got, "ID;") {
		t.Fatalf("control: os.Mkdir must inherit its parent's list, got %s", got)
	}
	if err := mkdirPrivate(private); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WHOSAIDSO_ACL_PATH", private)
	got := sddl(t, private)
	dacl := got[strings.Index(got, "D:"):]
	if want := rendered(t, "D:P(A;OICI;FA;;;"+user+")"); dacl != want {
		t.Fatalf("private directory list = %s, want %s", dacl, want)
	}
	if err := requireOwnerOnly(private, true); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(private, "packet.json")
	putFile(t, child, []byte("{}"))
	t.Setenv("WHOSAIDSO_ACL_PATH", child)
	if got := sddl(t, child); !strings.HasSuffix(got, rendered(t, "D:(A;ID;FA;;;"+user+")")) {
		t.Fatalf("a file created inside must inherit only the user's entry, got %s", got)
	}
}

// A writer that publishes the destination after the publisher's existence
// check and before its rename is caught by the rename itself on Windows, and
// the bundle it published is left exactly as it was.
func TestPublishCatchesAWriterBetweenCheckAndRename(t *testing.T) {
	p := ledgerProject(t)
	admitControl(t, p, 1)
	planted := []byte("bytes another writer admitted\n")
	disk := systemPublishIO()
	var destination string
	disk.rename = func(from, to string) error {
		destination = to
		if err := os.WriteFile(to, planted, 0o644); err != nil {
			return err
		}
		return renameNoReplace(from, to)
	}
	bundles, err := readLedger(p)
	if err != nil {
		t.Fatal(err)
	}
	prefix := State{project: p, bundles: bundles}
	proposed, _ := proposeFor(2)(nil)
	bundle, err := sealBundle(p, prefix.chain, admissionID(2), digestFor(2), proposed)
	if err != nil {
		t.Fatal(err)
	}
	requireFault(t, publishBundle(context.Background(), p.Ledger, bundle, disk), "ledger-fork")
	if kept, err := os.ReadFile(destination); err != nil || string(kept) != string(planted) {
		t.Fatalf("the racing writer's bundle was overwritten: %q, %v", kept, err)
	}
	requireNoTemporaries(t, p)
}

// A NULL access list grants everyone everything; it must read as the opposite
// of owner-only, not as an empty list of grants.
func TestNullAccessListIsRefused(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "private")
	if err := mkdirPrivate(dir); err != nil {
		t.Fatal(err)
	}
	if err := requireOwnerOnly(dir, true); err != nil {
		t.Fatalf("control: %v", err)
	}
	t.Setenv("WHOSAIDSO_ACL_PATH", dir)
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command",
		"$i = Get-Item -LiteralPath $env:WHOSAIDSO_ACL_PATH; $a = $i.GetAccessControl(); "+
			"$a.SetSecurityDescriptorSddlForm('D:NO_ACCESS_CONTROL', 'Access'); $i.SetAccessControl($a)").CombinedOutput()
	if err != nil {
		t.Fatalf("set a NULL list: %v: %s", err, out)
	}
	// .NET renders an absent list by leaving out the D: section entirely.
	if got := sddl(t, dir); strings.Contains(got, "D:") && !strings.Contains(got, "D:NO_ACCESS_CONTROL") {
		t.Fatalf("control: the fixture must hold a NULL list, got %s", got)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	requireFault(t, privateAccessFault(dir, info, true), "insecure-permissions")
}

// holdExclusive opens path sharing nothing, as the kernel holds a file while
// renaming it, and returns the release.
func holdExclusive(t *testing.T, path string) func() {
	t.Helper()
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ, 0, nil, syscall.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	release := func() { once.Do(func() { syscall.CloseHandle(h) }) }
	t.Cleanup(release)
	return release
}

func TestBundleReadWaitsOutASharingViolation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "00000001-bundle.json")
	putFile(t, path, []byte("{}"))
	release := holdExclusive(t, path)
	if _, err := os.ReadFile(path); err == nil {
		t.Fatal("control: a file held without sharing must refuse a plain read")
	}
	go func() {
		time.Sleep(50 * time.Millisecond)
		release()
	}()
	if data, err := readBundle(path); err != nil || string(data) != "{}" {
		t.Fatalf("a handle released within the bound must not fail the read: %q, %v", data, err)
	}
}

func TestBundleReadGivesUpOnAHandleHeldForGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "00000001-bundle.json")
	putFile(t, path, []byte("{}"))
	holdExclusive(t, path)
	if _, err := readBundle(path); err == nil {
		t.Fatal("a handle that is never released must surface the sharing violation")
	}
}
