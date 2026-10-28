package parallel

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestDoRunsEachIndexOnceAndReportsTheLowestFailure(t *testing.T) {
	var runs [200]atomic.Int32
	if err := Do(len(runs), 0, func(i int) error { runs[i].Add(1); return nil }); err != nil {
		t.Fatal(err)
	}
	for i := range runs {
		if runs[i].Load() != 1 {
			t.Fatalf("index %d ran %d times", i, runs[i].Load())
		}
	}
	// The later failure finishes first; the answer must still be the one a
	// sequential loop stops at.
	low, high := errors.New("low"), errors.New("high")
	err := Do(200, 3, func(i int) error {
		switch i {
		case 7:
			time.Sleep(50 * time.Millisecond)
			return low
		case 150:
			return high
		}
		return nil
	})
	if err != low {
		t.Fatalf("got %v, want the lowest failing index's error", err)
	}
	if err := Do(0, 0, func(int) error { return errors.New("never") }); err != nil {
		t.Fatalf("empty run: %v", err)
	}
}
