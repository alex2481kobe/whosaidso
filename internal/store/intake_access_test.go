package store

// What a capture acknowledges must pass the owner-only check a later read
// applies: staging that widens while it is written is refused before the
// rename that publishes it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesStagingWidenedBeforePublication(t *testing.T) {
	for _, target := range []string{"staging", "packet.json"} {
		t.Run(target, func(t *testing.T) {
			p := intakeProject(t)
			directory := target == "staging"
			disk := systemIntakeIO()
			widened := ""
			// The flush of the staging directory and of packet.json is the
			// last moment before publication at which each can be widened.
			disk.sync = func(f *os.File) error {
				name := f.Name()
				info, err := os.Stat(name)
				staging := err == nil && info.IsDir() && strings.HasSuffix(name, ".tmp")
				if widened == "" && (directory && staging || !directory && filepath.Base(name) == "packet.json") {
					widened = name
					widenAccess(t, name, directory)
				}
				return f.Sync()
			}
			_, err := writeIntake(context.Background(), p, requestFor(commandID(1), "source"), disk)
			if widened == "" {
				t.Fatalf("%s was never flushed", target)
			}
			requireFault(t, err, "insecure-permissions")
			packets, err := ReadIntake(p, nil)
			if err != nil || len(packets) != 0 {
				t.Fatalf("a refused capture published %+v, %v", packets, err)
			}
		})
	}
}
