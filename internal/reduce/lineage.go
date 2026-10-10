package reduce

// The ledger's chain structure lives here, once: the canonical order bundles
// must arrive in, where the sequence numbers may not have a hole, which bundle
// each one names as its predecessor, and one command id published twice. Both
// the fold and the store's raw ledger read check a ledger through this, so the
// two can never disagree about what a well-formed chain is. What an event
// means is not here.

import (
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// Lineage is the chain structure of a ledger prefix read in canonical order.
// The zero value is the empty ledger.
type Lineage struct {
	last  model.BundleRef
	sites map[model.ID][]CommandSite
}

// CommandSite is one bundle published under a command id: where, and with
// which request digest, so a repeated id can say whether the request repeated.
type CommandSite struct {
	Sequence uint64       `json:"sequence"`
	Digest   model.Digest `json:"digest"`
}

func newLineage() Lineage { return Lineage{sites: map[model.ID][]CommandSite{}} }

// Add checks that b may follow everything added so far and records it.
func (l *Lineage) Add(b model.Bundle) error { return l.add(b) }

func (l *Lineage) add(b model.Bundle) error {
	ref := b.Ref()
	empty := l.last == model.BundleRef{}
	switch {
	case empty && b.Sequence != 1:
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.sequence", "sequence 1 is missing")
	case !empty && !l.last.Before(ref):
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.sequence",
			fmt.Sprintf("out of canonical order: follows sequence %d command %s", l.last.Sequence, l.last.CommandID))
	case !empty && b.Sequence > l.last.Sequence+1:
		// Contiguity, not merely monotonicity. A gap means a bundle we never
		// read, and a fold over an incomplete prefix is wrong in a way no later
		// event repairs.
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.sequence",
			fmt.Sprintf("sequence %d is missing", l.last.Sequence+1))
	}
	if b.Sequence == 1 {
		if b.Predecessor != "" {
			return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.predecessor",
				"the genesis bundle has no predecessor")
		}
		if !empty {
			return faultAt(CodeLedgerFork, b.Sequence, -1, "bundle.sequence",
				fmt.Sprintf("sequence 1 is published twice, also by command %s", l.last.CommandID))
		}
	} else if b.Sequence == l.last.Sequence {
		return faultAt(CodeLedgerFork, b.Sequence, -1, "bundle.sequence",
			fmt.Sprintf("sequence %d is published twice, also by command %s", b.Sequence, l.last.CommandID))
	} else if b.Predecessor != l.last.CommandID {
		// Sequence alone only counts files. The predecessor is what makes the
		// chain say which bundle each one was actually appended to.
		return faultAt(CodeLedgerDiscontinuity, b.Sequence, -1, "bundle.predecessor",
			fmt.Sprintf("expected predecessor %s", l.last.CommandID))
	}
	if sites := l.sites[b.CommandID]; len(sites) > 0 {
		return duplicateCommand(b, sites[0])
	}
	if l.sites == nil {
		l.sites = map[model.ID][]CommandSite{}
	}
	l.sites[b.CommandID] = []CommandSite{{Sequence: b.Sequence, Digest: b.RequestDigest}}
	l.last = ref
	return nil
}

func duplicateCommand(b model.Bundle, prior CommandSite) error {
	requests := "matching request digests"
	if prior.Digest != b.RequestDigest {
		requests = "different request digests"
	}
	return faultAt(CodeDuplicateCommand, b.Sequence, -1, "bundle.command_id",
		fmt.Sprintf("command id %s is published at sequences %d and %d with %s; admission identity is ambiguous",
			b.CommandID, prior.Sequence, b.Sequence, requests))
}

// lineageFields are the lineage's persisted parts, named for the image.
func (l *Lineage) fields(prefix string) []persistedField {
	return []persistedField{{prefix + ".last", &l.last}, {prefix + ".sites", &l.sites}}
}

func (l Lineage) clone() Lineage {
	return Lineage{last: l.last, sites: copyMap(l.sites)}
}
