package reduce

// Internal fixtures exported to this directory's external tests. Those tests
// publish through store, which imports reduce, so they cannot be internal.

import (
	"testing"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

const TestProject = testProject

var (
	BaseTime          = baseTime
	NewID             = newID
	NewDigest         = newDigest
	SealProof         = sealProof
	FixtureProvenance = provenance
)

func FixtureTaskSpec() model.TaskSpec { return taskSpec() }

func ProofUnknownCriterion() model.Availability[model.CriterionRef] {
	return proofUnknown[model.CriterionRef]()
}

// FixtureLedger is the internal tests' ledger builder.
type FixtureLedger = ledgerBuilder

func NewFixtureLedger() *FixtureLedger { return newLedger() }

func SealStart(t *testing.T) (*FixtureLedger, model.InvocationEnvelope, Snapshot) {
	return sealStart(t)
}

func (l *ledgerBuilder) Add(t *testing.T, events ...model.TypedEvent) model.Bundle {
	t.Helper()
	return l.add(t, events...)
}

func (l *ledgerBuilder) Bundles() []model.Bundle { return l.out }

// After continues the builder's chain from a bundle published elsewhere.
func (l *ledgerBuilder) After(b model.Bundle) { l.seq, l.prev = b.Sequence, b.CommandID }
