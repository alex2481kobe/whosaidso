package main

import (
	"bytes"
	"os"
	"os/exec"
	"testing"

	"datum/internal/reduce"
	"datum/internal/store"
)

func TestDatumMainProcess(t *testing.T) {
	if os.Getenv("DATUM_MAIN_TEST_PROCESS") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"datum"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	os.Exit(2)
}

func TestDatumFreshProcessesCaptureAdmitReplay(t *testing.T) {
	root, input := cliFixture(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	invoke := func(data []byte, args ...string) {
		t.Helper()
		command := exec.Command(binary, append([]string{"-test.run=^TestDatumMainProcess$", "--"}, args...)...)
		command.Dir = root
		command.Env = append(os.Environ(), "DATUM_MAIN_TEST_PROCESS=1")
		command.Stdin = bytes.NewReader(data)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("fresh CLI process failed: %s\n%v", output, err)
		}
	}
	invoke(input, "capture", "--command-id", string(cliID(3)), "--actor", "lane")
	project, err := store.Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	prefix, err := store.ReadPrefix(project)
	if err != nil || len(prefix) != 0 {
		t.Fatalf("capture published directly: %v, %v", prefix, err)
	}
	invoke(nil, "admit", "--command-id", string(cliID(4)), "--actor", "coordinator", "--outcome", "accepted", "--reason", "verified in a fresh process", string(cliID(3)))
	prefix, err = store.ReadPrefix(project)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := reduce.Replay(prefix)
	if err != nil || len(snapshot.Records()) != 1 || snapshot.Watermark().Sequence != 1 {
		t.Fatalf("admitted task did not survive process exit: %+v, %v", snapshot.Records(), err)
	}
}
