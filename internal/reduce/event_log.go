package reduce

// The admitted events' ledger order and the type-specific inventories that
// projections scan live here. What any event means, and the queries built on
// these inventories, do not.

import "datum/internal/model"

// eventLog lists admitted origins in ledger order. apply adds each event as it
// is admitted, and bundles apply in sequence with their events in index order,
// so appending IS ledger order: nothing is sorted, and a snapshot's log holds
// exactly its own prefix, including events already applied from a bundle that
// is still being applied. The typed lists are subsequences of all.
type eventLog struct {
	all               []Origin
	losses            []Origin // correction, trust.withdraw, supersede, artifact.dispose
	proofs            []Origin
	decisionDisposals []Origin
	supersessions     []Origin
	corrections       []Origin
	withdrawals       []Origin
	disposals         []Origin
}

func (l *eventLog) add(o Origin, e model.TypedEvent) {
	l.all = append(l.all, o)
	switch e.(type) {
	case *model.ProofAdmit:
		l.proofs = append(l.proofs, o)
	case *model.DecisionDispose:
		l.decisionDisposals = append(l.decisionDisposals, o)
	case *model.Supersede:
		l.supersessions = append(l.supersessions, o)
		l.losses = append(l.losses, o)
	case *model.Correction:
		l.corrections = append(l.corrections, o)
		l.losses = append(l.losses, o)
	case *model.TrustWithdraw:
		l.withdrawals = append(l.withdrawals, o)
		l.losses = append(l.losses, o)
	case *model.ArtifactDispose:
		l.disposals = append(l.disposals, o)
		l.losses = append(l.losses, o)
	}
}

// fork caps every list at its length, so the first append on either side
// reallocates instead of writing into a backing array the other still reads.
// Two Applys onto one snapshot therefore never see each other's events.
func (l eventLog) fork() eventLog {
	clip := func(o []Origin) []Origin { return o[:len(o):len(o)] }
	return eventLog{
		all:               clip(l.all),
		losses:            clip(l.losses),
		proofs:            clip(l.proofs),
		decisionDisposals: clip(l.decisionDisposals),
		supersessions:     clip(l.supersessions),
		corrections:       clip(l.corrections),
		withdrawals:       clip(l.withdrawals),
		disposals:         clip(l.disposals),
	}
}
