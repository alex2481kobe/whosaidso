package store

// Tests for NoCacheEnv (snapshot.go): set to 1, Load is the full ledger read
// and neither reads nor writes an image; any other non-empty value refuses.

import (
	"bytes"
	"context"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func TestNoCacheLoadIsTheFullReadAndTouchesNoImage(t *testing.T) {
	p := warmProject(t)
	requireReplayed(t, p, 4) // control: the default path restores the image
	image := imageBytes(t, p)
	t.Setenv(NoCacheEnv, "1")
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	full, err := Replayed(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Restored() != 0 || !reflect.DeepEqual(s.Snapshot(), full.Snapshot()) {
		t.Fatalf("with %s=1 Load must be the full read: restored %d", NoCacheEnv, s.Restored())
	}
	// An admission under the switch publishes to the ledger and writes no image.
	if _, err := Transact(context.Background(), p, admissionID(5), digestFor(5), cacheTask(5)); err != nil {
		t.Fatal(err)
	}
	if got := imageBytes(t, p); !bytes.Equal(got, image) {
		t.Fatalf("with %s=1 the cache image must not be written", NoCacheEnv)
	}
	// A missing image is not created either.
	if err := os.Remove(imagePath(p)); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(imagePath(p)); !os.IsNotExist(err) {
		t.Fatalf("with %s=1 no image may be created: %v", NoCacheEnv, err)
	}
}

func TestNoCacheRefusesAnUndefinedValue(t *testing.T) {
	p := warmProject(t)
	for _, value := range []string{"0", "true", "1 ", "yes"} {
		t.Setenv(NoCacheEnv, value)
		_, err := Load(p)
		var f *model.Fault
		if !errors.As(err, &f) || f.Path != NoCacheEnv {
			t.Errorf("%s=%q must be refused by name, got %v", NoCacheEnv, value, err)
		}
	}
}
