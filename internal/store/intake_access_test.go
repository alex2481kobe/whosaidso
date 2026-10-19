package store

// What a capture acknowledges must pass the owner-only check a later read
// applies: any part of staging that widens while it is written (the staging
// directory, packet.json, the blobs directory or a blob) is refused before
// the rename that publishes it.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCaptureRefusesStagingWidenedBeforePublication(t *testing.T) {
	for _, target := range []string{"staging", "packet.json", "blobs", "blob"} {
		t.Run(target, func(t *testing.T) {
			p := intakeProject(t)
			directory := target == "staging" || target == "blobs"
			disk := systemIntakeIO()
			widened := ""
			// Each path's flush is the last moment before publication at which
			// it can be widened.
			disk.sync = func(f *os.File) error {
				name, base := f.Name(), filepath.Base(f.Name())
				info, err := os.Stat(name)
				hit := map[string]bool{
					"staging":     err == nil && info.IsDir() && strings.HasSuffix(name, ".tmp"),
					"packet.json": base == "packet.json",
					"blobs":       base == "blobs",
					"blob":        strings.HasPrefix(base, "blob-"),
				}[target]
				if widened == "" && hit {
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
