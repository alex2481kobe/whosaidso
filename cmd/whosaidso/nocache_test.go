package main

// WHOSAIDSO_NO_CACHE through fresh CLI processes: set to 1, a read is the full
// ledger read and a deliberately rewritten cache image changes nothing; any
// other value is refused by name. The loader's own cases are in
// internal/store (nocache_test.go).

import (
	"bytes"
	"crypto/sha256"
	"os"
	"os/exec"
	"strings"
	"testing"

	"whosaidso/internal/store"
)

func TestNoCacheReadsTheLedgerPastATamperedImage(t *testing.T) {
	root, data := cliFixture(t)
	if _, errs, code := cliRun(t, root, data, "agent", "capture", "--command-id", string(cliID(3))); code != 0 {
		t.Fatalf("control capture: %d %s", code, errs)
	}
	if _, errs, code := cliRun(t, root, nil, "coordinator", "admit", "--command-id", string(cliID(4)), "--outcome", "accepted", "--reason", "fixture", string(cliID(3))); code != 0 {
		t.Fatalf("control admit: %d %s", code, errs)
	}
	want := readProcess(t, root, nil, "show", "--json", string(cliID(1)))
	image := cacheImage(t, root)
	raw, err := os.ReadFile(image)
	if err != nil {
		t.Fatalf("control: the read must leave a cache image: %v", err)
	}
	// Equal length keeps the image's framing; the checksum is recomputed, so
	// only a full ledger read can tell the image lies.
	forged := bytes.ReplaceAll(raw, []byte("exercise the CLI"), []byte("forged the cache"))
	head := len("whosaidso-cache/1\n")
	sum := sha256.Sum256(forged[head+sha256.Size:])
	copy(forged[head:head+sha256.Size], sum[:])
	if bytes.Equal(raw, forged) {
		t.Fatal("fixture did not find the cached intent")
	}
	if err := os.WriteFile(image, forged, 0o600); err != nil {
		t.Fatal(err)
	}
	// Control: the default path believes the rewritten image (the declared
	// blind spot), so the fixture really reaches what NoCacheEnv must bypass.
	if got := readProcess(t, root, nil, "show", "--json", string(cliID(1))); !bytes.Contains(got, []byte("forged the cache")) {
		t.Fatalf("control: the tampered image was not read on the default path:\n%s", got)
	}
	t.Setenv(store.NoCacheEnv, "1")
	if got := readProcess(t, root, nil, "show", "--json", string(cliID(1))); !bytes.Equal(got, want) {
		t.Errorf("with %s=1 show --json must be the ledger's answer, got:\n%s\nwant:\n%s", store.NoCacheEnv, got, want)
	}
	if after, err := os.ReadFile(image); err != nil || !bytes.Equal(after, forged) {
		t.Errorf("with %s=1 the cache must not be written: %v", store.NoCacheEnv, err)
	}
	for _, value := range []string{"0", "true", "yes", " 1"} {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		command := exec.Command(binary, "-test.run=^TestWhoSaidSoMainProcess$", "--", "show", "--json", string(cliID(1)))
		command.Dir = root
		command.Env = append(os.Environ(), "WHOSAIDSO_MAIN_TEST_PROCESS=1", store.NoCacheEnv+"="+value)
		out, err := command.CombinedOutput()
		if err == nil || !strings.Contains(string(out), store.NoCacheEnv) {
			t.Errorf("%s=%q must be refused by name, never ignored: err=%v\n%s", store.NoCacheEnv, value, err, out)
		}
	}
}
