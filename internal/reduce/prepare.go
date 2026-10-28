package reduce

// What the fold derives from one raw event and its bundle's project alone,
// computed ahead of the fold so a full replay can do it on several goroutines.
// Nothing here reads or writes state: decoding, schema validation and the
// reference walk are pure, and the fold still takes every refusal in its own
// order, from the stored results.

import (
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/parallel"
)

// preparedEvent is one event decoded, with its same-project references. A
// decode failure leaves typed nil; a reference failure is the fold's to
// return at the point it would have found it.
type preparedEvent struct {
	typed   model.TypedEvent
	err     error
	refs    []model.Reference
	refsErr error
}

func prepareBundle(b model.Bundle) []preparedEvent {
	out := make([]preparedEvent, len(b.Events))
	for i, raw := range b.Events {
		out[i].typed, out[i].err = model.DecodeEvent(raw)
		if out[i].err == nil {
			out[i].refs, out[i].refsErr = model.SameProjectReferences(out[i].typed, b.Project)
		}
	}
	return out
}

// replayChunk bounds how many bundles are prepared at once, so a long ledger
// holds a window of decoded events beyond the state's own, not all of them.
const replayChunk = 256

// replayInto folds bundles onto st in order, preparing each chunk on several
// goroutines first. The fold itself stays sequential and stops at the first
// refusal, exactly as a plain loop of st.apply would.
func replayInto(st *state, bundles []model.Bundle) error {
	for start := 0; start < len(bundles); start += replayChunk {
		chunk := bundles[start:min(start+replayChunk, len(bundles))]
		prepared := make([][]preparedEvent, len(chunk))
		parallel.Do(len(chunk), 0, func(i int) error {
			prepared[i] = prepareBundle(chunk[i])
			return nil
		})
		for i := range chunk {
			st.prepared = prepared[i]
			err := st.apply(chunk[i])
			st.prepared = nil
			if err != nil {
				return err
			}
		}
	}
	return nil
}
