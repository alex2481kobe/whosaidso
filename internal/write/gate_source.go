package write

// A speaker's message has one place in their own sequence: admission refuses
// a source.intake whose known speaker and order an admitted source, or one
// earlier in the same admission, already holds. Ledgers that hold such a
// pair from before still replay; two branches that each captured one are
// the reducer's clash (reduce/branch_clash.go). An unknown speaker has no
// sequence to collide in: model.SameActor never matches one.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/reduce"
)

func gateSourceOrder(sources []reduce.Source, e *model.SourceIntake) error {
	var held *reduce.Source
	next := uint64(0)
	for i, src := range sources {
		if !model.SameActor(src.Intake.Speaker, e.Speaker) {
			continue
		}
		if src.Intake.Order >= next {
			next = src.Intake.Order + 1
		}
		if src.Intake.Order == e.Order && held == nil {
			held = &sources[i]
		}
	}
	if held == nil {
		return nil
	}
	return admissionFault(reduce.CodeDuplicateSourceOrder, "order", fmt.Sprintf(
		"%s's message %d is already captured as source %s; if this is that message, it is already in the ledger; if it is another, its order is its place in %s's sequence (the next unused is %d)",
		e.Speaker.ID, e.Order, held.Key.Source, e.Speaker.ID, next))
}
