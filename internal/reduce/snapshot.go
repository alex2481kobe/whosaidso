package reduce

// Snapshot reads, defensive copies at the read boundary, and stable ordering live here.
// Replay and state transitions do not.

import (
	"sort"

	"whosaidso/internal/model"
)

// ---- Snapshot ------------------------------------------------------------

// Snapshot is an immutable projection of a complete ledger prefix. The zero
// value is the empty snapshot, which is a legitimate answer for a new project.
type Snapshot struct{ st *state }

func (s Snapshot) inner() *state {
	if s.st == nil {
		return newState()
	}
	return s.st
}

// ---- accessors -----------------------------------------------------------

// Project is the declared project this ledger belongs to, empty for an empty
// snapshot.
func (s Snapshot) Project() model.ProjectID { return s.inner().project }

// Watermark is the selected ledger position this snapshot answers from.
func (s Snapshot) Watermark() Watermark { return s.inner().watermark }

// Record returns one exact admitted revision.
func (s Snapshot) Record(ref model.RecordRef) (Record, bool) {
	r, ok := s.inner().record(ref)
	return deepCopy(r), ok
}

// record is the uncopied read. Callers inside this package scan without
// paying for a copy they are about to discard; only the exported accessor,
// where a value leaves the package, copies.
func (s *state) record(ref model.RecordRef) (Record, bool) {
	r, ok := s.records[recordKey(ref)]
	return r, ok
}

// CurrentRevision is the highest admitted revision of a record.
func (s Snapshot) CurrentRevision(id Ident) (model.Revision, bool) {
	r, ok := s.inner().current[id]
	return r, ok
}

// Current returns a record at its current revision.
func (s Snapshot) Current(id Ident) (Record, bool) {
	r, ok := s.inner().currentRecord(id)
	return deepCopy(r), ok
}

func (s *state) currentRecord(id Ident) (Record, bool) {
	rev, ok := s.current[id]
	if !ok {
		return Record{}, false
	}
	r, ok := s.records[RecordKey{Project: id.Project, ID: id.ID, Revision: rev}]
	return r, ok
}

// Records returns every admitted revision, sorted by project, id then revision.
// Sorting is not cosmetic: a map range would make the answer depend on Go's
// randomized iteration, and two runs of the same ledger must agree exactly.
func (s Snapshot) Records() []Record {
	return deepCopySlice(s.inner().recordsSorted())
}

func (s *state) recordsSorted() []Record {
	out := make([]Record, 0, len(s.records))
	for _, r := range s.records {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return lessRecordKey(out[i].Key, out[j].Key) })
	return out
}

func lessRecordKey(a, b RecordKey) bool {
	if a.Project != b.Project {
		return a.Project < b.Project
	}
	if a.ID != b.ID {
		return a.ID < b.ID
	}
	return a.Revision < b.Revision
}

func (s *state) attemptsFor(id Ident) []Attempt {
	out := []Attempt{}
	for _, a := range s.attempts {
		if a.Key.Project == id.Project && a.Key.Task == id.ID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Started != out[j].Started {
			return out[i].Started.before(out[j].Started)
		}
		return out[i].Key.Attempt < out[j].Key.Attempt
	})
	return out
}

func (s *state) blockersFor(id Ident) []Blocker {
	out := []Blocker{}
	for _, bl := range s.blockers {
		if bl.Key.Project == id.Project && bl.Key.Task == id.ID {
			out = append(out, bl)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Held != out[j].Held {
			return out[i].Held.before(out[j].Held)
		}
		return out[i].Key.Blocker < out[j].Key.Blocker
	})
	return out
}

// Attempts returns one task's attempts in ledger order.
func (s Snapshot) Attempts(id Ident) []Attempt { return deepCopySlice(s.inner().attemptsFor(id)) }

// Blockers returns one task's holds in ledger order, cleared ones included.
func (s Snapshot) Blockers(id Ident) []Blocker { return deepCopySlice(s.inner().blockersFor(id)) }

// Closure returns the admitted closure, whether or not it takes effect.
func (s Snapshot) Closure(id Ident) (Closure, bool) {
	c, ok := s.inner().closed[id]
	return deepCopy(c), ok
}

// Invocation returns one start/seal pair.
func (s Snapshot) Invocation(key InvocationKey) (Invocation, bool) {
	i, ok := s.inner().invocations[key]
	return deepCopy(i), ok
}

// Invocations returns every invocation, sorted by project then id.
func (s Snapshot) Invocations() []Invocation {
	return deepCopySlice(s.inner().invocationsSorted())
}

func (s *state) invocationsSorted() []Invocation {
	out := make([]Invocation, 0, len(s.invocations))
	for _, i := range s.invocations {
		out = append(out, i)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Project != out[j].Key.Project {
			return out[i].Key.Project < out[j].Key.Project
		}
		return out[i].Key.InvocationID < out[j].Key.InvocationID
	})
	return out
}

// Criterion returns one frozen predicate at its exact claim and criterion
// revisions.
func (s Snapshot) Criterion(ref model.CriterionRef) (Criterion, bool) {
	c, ok := s.inner().criteria[criterionKey(ref)]
	return deepCopy(c), ok
}

// Review returns the admitted disposition of one intake packet.
func (s Snapshot) Review(key ReviewKey) (Review, bool) {
	r, ok := s.inner().reviews[key]
	return deepCopy(r), ok
}

// Reviews returns all dispositions sorted by project then packet command ID.
// Audits can select SelfAdmission without intake bytes or reason parsing.
func (s Snapshot) Reviews() []Review {
	out := make([]Review, 0, len(s.inner().reviews))
	for _, r := range s.inner().reviews {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Project != out[j].Key.Project {
			return out[i].Key.Project < out[j].Key.Project
		}
		return out[i].Key.CommandID < out[j].Key.CommandID
	})
	return deepCopySlice(out)
}

// ReviewsInLedgerOrder returns every disposition in the order the ledger
// admitted it: by its review event's origin, then by the packet's position in
// that event. It is the order a walk over the prefix's review.admit events
// meets them, answered from the snapshot so a reader needs no raw bundles.
func (s Snapshot) ReviewsInLedgerOrder() []Review {
	st := s.inner()
	out := make([]Review, 0, len(st.reviews))
	position := make(map[ReviewKey]int, len(st.reviews))
	for _, r := range st.reviews {
		out = append(out, r)
		if e, ok := st.events[r.Origin].(*model.ReviewAdmit); ok {
			for i, p := range e.Packets {
				if p.CommandID == r.Key.CommandID {
					position[r.Key] = i
					break
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin.before(out[j].Origin)
		}
		return position[out[i].Key] < position[out[j].Key]
	})
	return deepCopySlice(out)
}

// Sources returns every captured source, sorted by project then id.
func (s Snapshot) Sources() []Source {
	return deepCopySlice(s.inner().sourcesSorted())
}

func (s *state) sourcesSorted() []Source {
	out := make([]Source, 0, len(s.sources))
	for _, v := range s.sources {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Key.Project != out[j].Key.Project {
			return out[i].Key.Project < out[j].Key.Project
		}
		return out[i].Key.Source < out[j].Key.Source
	})
	return out
}

func sortedReferrers(in []Referrer) []Referrer {
	out := deepCopySlice(in)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Origin != out[j].Origin {
			return out[i].Origin.before(out[j].Origin)
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// RecordReferrers returns every admitted event that named this exact revision.
func (s Snapshot) RecordReferrers(ref model.RecordRef) []Referrer {
	return sortedReferrers(s.inner().reverseRecord[recordKey(ref)])
}

// CriterionReferrers returns every admitted event that named this criterion.
func (s Snapshot) CriterionReferrers(ref model.CriterionRef) []Referrer {
	return sortedReferrers(s.inner().reverseCriterion[criterionKey(ref)])
}

// InvocationReferrers returns every admitted event that named this invocation.
func (s Snapshot) InvocationReferrers(ref model.InvocationRef) []Referrer {
	return sortedReferrers(s.inner().reverseInvocation[invocationKey(ref)])
}

// Deferred is empty because all named events now have reducer semantics.
func (s Snapshot) Deferred() []Deferred { return nil }
