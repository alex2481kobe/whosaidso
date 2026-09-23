package reduce

import (
	"fmt"
	"sort"

	"datum/internal/model"
)

// TaskStatus is one of the contract's four TASK statuses. The spelling matches
// the contract text so a rendered answer and the contract read the same.
type TaskStatus string

const (
	StatusClosed   TaskStatus = "CLOSED"
	StatusInFlight TaskStatus = "IN FLIGHT"
	StatusBlocked  TaskStatus = "BLOCKED"
	StatusReady    TaskStatus = "READY"
)

// Truth is a three-valued prerequisite result. UNKNOWN is a real answer and is
// never rounded to TRUE: an unresolved dependency that reads as satisfied is
// how a task gets dispatched into work nobody established was possible.
type Truth string

const (
	TruthTrue    Truth = "TRUE"
	TruthFalse   Truth = "FALSE"
	TruthUnknown Truth = "UNKNOWN"
)

// Reason kinds for a BLOCKED projection. These are structured rather than prose
// because each one is its own queue: "awaiting acceptance" and "waiting on a
// prerequisite" need different people to act.
const (
	ReasonPrerequisite       = "prerequisite"
	ReasonAwaitingAcceptance = "awaiting-acceptance"
	ReasonResume             = "resume"
	ReasonReconciliation     = "reconciliation"
)

// BlockedReason is one structured cause, with whoever is waited on. Actor may
// be the unknown branch: an unstated waiting actor is recorded as unknown, never
// guessed from whoever happens to be nearby.
type BlockedReason struct {
	Kind      string           `json:"kind"`
	Detail    string           `json:"detail"`
	Actor     model.Actor      `json:"actor"`
	BlockerID model.ID         `json:"blocker_id"`
	Target    *model.RecordRef `json:"target"`
	Truth     Truth            `json:"truth"`
	Origin    Origin           `json:"origin"`
}

// PrerequisiteResult is one typed prerequisite evaluated at the queried task
// revision. Waived records that a declared waiver carried the requirement, so a
// reader can tell "satisfied" from "excused" without reading the spec again.
type PrerequisiteResult struct {
	Index  int              `json:"index"`
	Kind   string           `json:"kind"`
	Target model.RecordRef  `json:"target"`
	Truth  Truth            `json:"truth"`
	Waived bool             `json:"waived"`
	Detail string           `json:"detail"`
	Waiver *model.Authority `json:"waiver"`
}

// Satisfied reports whether this prerequisite stops blocking the task.
func (p PrerequisiteResult) Satisfied() bool { return p.Truth == TruthTrue || p.Waived }

// TaskProjection is the whole answer for one task, derived only from admitted
// events. Closure is populated whenever one was admitted, including when it did
// not take effect, because hiding an ineffective closure hides who tried to
// close the task and why it did not stick.
type TaskProjection struct {
	Task          RecordKey            `json:"task"`
	Spec          *model.TaskSpec      `json:"spec"`
	Status        TaskStatus           `json:"status"`
	Outcome       model.ClosureOutcome `json:"outcome"`
	Closure       *Closure             `json:"closure"`
	Attempts      []Attempt            `json:"attempts"`
	LiveAttempts  []AttemptKey         `json:"live_attempts"`
	Blockers      []Blocker            `json:"blockers"`
	Prerequisites []PrerequisiteResult `json:"prerequisites"`
	Reasons       []BlockedReason      `json:"reasons"`
	WaitingActors []model.Actor        `json:"waiting_actors"`
	NextActor     model.Actor          `json:"next_actor"`
	CommitsDenied bool                 `json:"commits_denied"`
}

// Task projects one task at its current revision. The second result is false
// when no TASK with that identity is admitted, which is different from a task
// with nothing to say.
func (s Snapshot) Task(id Ident) (TaskProjection, bool) {
	p, ok := s.inner().task(id)
	return deepCopy(p), ok
}

// TaskAt projects one exact task revision: its prerequisites and acceptance
// criteria are that revision's, never the current one's. Attempts, holds and
// closure belong to the task identity and are shared by every revision.
func (s Snapshot) TaskAt(ref model.RecordRef) (TaskProjection, bool) {
	p, ok := s.inner().taskRevision(recordKey(ref))
	return deepCopy(p), ok
}

// Tasks projects every admitted task, sorted by project then id.
func (s Snapshot) Tasks() []TaskProjection {
	st := s.inner()
	ids := make([]Ident, 0, len(st.current))
	for who, rev := range st.current {
		if r, ok := st.records[RecordKey{Project: who.Project, ID: who.ID, Revision: rev}]; ok && r.Kind == model.Task {
			ids = append(ids, who)
		}
	}
	sort.Slice(ids, func(i, j int) bool {
		if ids[i].Project != ids[j].Project {
			return ids[i].Project < ids[j].Project
		}
		return ids[i].ID < ids[j].ID
	})
	out := make([]TaskProjection, 0, len(ids))
	for _, who := range ids {
		if p, ok := st.task(who); ok {
			out = append(out, p)
		}
	}
	return deepCopySlice(out)
}

// task evaluates the four predicates in the contract's order. The order is the
// point: BLOCKED and READY can both hold at once, for instance every
// prerequisite TRUE while reconciliation is owed, and dispatching that task is
// the failure the ordering exists to prevent.
func (s *state) task(id Ident) (TaskProjection, bool) {
	rev, ok := s.current[id]
	if !ok {
		return TaskProjection{}, false
	}
	return s.taskRevision(RecordKey{Project: id.Project, ID: id.ID, Revision: rev})
}

func (s *state) taskRevision(key RecordKey) (TaskProjection, bool) {
	id := Ident{Project: key.Project, ID: key.ID}
	rec, ok := s.records[key]
	if !ok || rec.Kind != model.Task {
		return TaskProjection{}, false
	}

	p := TaskProjection{
		Task:      key,
		Spec:      rec.Task,
		Attempts:  s.attemptsFor(id),
		Blockers:  s.blockersFor(id),
		NextActor: rec.Task.NextActor,
	}
	for _, a := range p.Attempts {
		if a.Live() {
			p.LiveAttempts = append(p.LiveAttempts, a.Key)
		}
		if a.Terminal != nil && a.Terminal.CommitsDenied {
			p.CommitsDenied = true
		}
	}
	closure, closed := s.closed[id]
	if closed {
		c := closure
		p.Closure = &c
	}

	// 1. An authorised closure exists and every attempt is terminal.
	if closed && len(p.LiveAttempts) == 0 {
		_, witnessed := s.witnessed(rec, closure)
		if closure.Outcome != model.ClosureSuccess || witnessed {
			p.Status = StatusClosed
			p.Outcome = closure.Outcome
			return p, true
		}
		// A success closure without applicable witnesses does not close the
		// obligation. It falls through to BLOCKED below carrying exactly which
		// witness is absent or stale, which is the queue an acceptance owner
		// works from.
	}

	// 2. An admitted start or takeover has no terminal receipt. Presumed
	// process liveness is not consulted: a holder is not proof of a heartbeat.
	if len(p.LiveAttempts) > 0 {
		p.Status = StatusInFlight
		return p, true
	}

	// 3 and 4. The obligation is open. Everything owed is collected first, then
	// BLOCKED wins over READY if anything at all is owed.
	p.Prerequisites = s.prerequisites(rec)
	p.Reasons = s.owed(id, rec, closure, closed, p.Prerequisites, p.Attempts)
	if len(p.Reasons) > 0 {
		p.Status = StatusBlocked
		p.WaitingActors = waitingActors(p.Reasons)
		return p, true
	}
	p.Status = StatusReady
	return p, true
}

// witnessed checks a success closure against the acceptance criteria of the
// revision it closed. A witness pinned to criterion revision 1 does not satisfy
// criterion revision 2: that is a stale witness, and accepting it would let an
// old sign-off certify a contract that has since changed.
func (s *state) witnessed(rec Record, c Closure) (missing []model.AcceptanceCriterion, ok bool) {
	for _, want := range rec.Task.AcceptanceCriteria {
		found := false
		for _, w := range c.AcceptanceWitnesses {
			if w.CriterionID == want.ID && w.CriterionRevision == want.Revision {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, want)
		}
	}
	// Delivery is a separate requirement from acceptance: a criterion can be
	// judged met while nothing was actually handed over.
	return missing, len(missing) == 0 && len(c.DeliveryWitnesses) > 0
}

// closedSuccess is the dependency rule, and it is deliberately not a call back
// into task(): a prerequisite chain can be cyclic, and this predicate needs no
// recursion. Nothing here reads status == CLOSED on its own, because cancelled,
// withdrawn and waived are closed too.
func (s *state) closedSuccess(target model.RecordRef) (Truth, string) {
	if target.Project != s.project {
		return TruthUnknown, "cross-project dependency resolves at read time"
	}
	rec, ok := s.records[recordKey(target)]
	if !ok {
		return TruthUnknown, fmt.Sprintf("no admitted revision %d of %s", target.Revision, target.RecordID)
	}
	if rec.Kind != model.Task {
		return TruthFalse, fmt.Sprintf("%s is a %s, not a TASK", target.RecordID, rec.Kind)
	}
	who := ident(target)
	c, closed := s.closed[who]
	if !closed {
		return TruthFalse, "the dependency is not closed"
	}
	for _, a := range s.attemptsFor(who) {
		if a.Live() {
			return TruthFalse, fmt.Sprintf("attempt %s has no terminal receipt", a.Key.Attempt)
		}
	}
	if c.Outcome != model.ClosureSuccess {
		return TruthFalse, fmt.Sprintf("closed %s, which is not success", c.Outcome)
	}
	// The producer must be CLOSED at its current revision; a historical
	// witness alone does not establish that its current obligation is met.
	current := s.records[RecordKey{Project: who.Project, ID: who.ID, Revision: s.current[who]}]
	if _, ok := s.witnessed(current, c); !ok {
		return TruthFalse, "the dependency's current revision is not witnessed closed"
	}
	// The witness must apply to the revision the consumer required, not merely
	// to whatever revision happened to be closed. rec is that exact revision.
	if missing, ok := s.witnessed(rec, c); !ok {
		if len(missing) > 0 {
			return TruthFalse, fmt.Sprintf("closure does not witness acceptance criterion %s revision %d",
				missing[0].ID, missing[0].Revision)
		}
		return TruthFalse, "closure carries no delivery witness"
	}
	return TruthTrue, ""
}

func (s *state) prerequisites(rec Record) []PrerequisiteResult {
	out := make([]PrerequisiteResult, 0, len(rec.Task.Prerequisites))
	for i, r := range rec.Task.Prerequisites {
		res := PrerequisiteResult{Index: i, Kind: r.Kind, Target: r.Target}
		switch r.Kind {
		case "task-success":
			res.Truth, res.Detail = s.closedSuccess(r.Target)
		case "claim-proof":
			res.Truth, res.Detail = s.claimProof(r.Target)
		case "decision-approved":
			res.Truth, res.Detail = s.decisionApproved(r.Target)
		default:
			// The model refuses any other kind at decode; an unrecognised one
			// is UNKNOWN, never TRUE, so it blocks rather than dispatches.
			res.Truth = TruthUnknown
			res.Detail = "unrecognised prerequisite kind " + r.Kind
		}
		// A waiver counts only where the consumer explicitly permits one AND
		// cites authority. A "forbid" policy is not overridable by anyone, and
		// the waiver stays visible rather than being folded into TRUE.
		if res.Truth != TruthTrue && r.WaiverPolicy == "allow-with-authority" && r.Authority != nil {
			res.Waived = true
			res.Waiver = r.Authority
		}
		out = append(out, res)
	}
	return out
}

// owed collects every structured reason the task cannot be dispatched. An empty
// prerequisite set is vacuously TRUE, so a task with no prerequisites and
// nothing else owed reaches READY.
func (s *state) owed(id Ident, rec Record, closure Closure, closed bool, prereqs []PrerequisiteResult, attempts []Attempt) []BlockedReason {
	reasons := []BlockedReason{}

	for _, r := range prereqs {
		if r.Satisfied() {
			continue
		}
		target := r.Target
		reasons = append(reasons, BlockedReason{
			Kind:   ReasonPrerequisite,
			Detail: fmt.Sprintf("prerequisite %d (%s) is %s: %s", r.Index, r.Kind, r.Truth, r.Detail),
			Actor:  s.dependencyActor(target),
			Target: &target,
			Truth:  r.Truth,
			Origin: rec.Origin,
		})
	}

	// An open hold is owed by definition, whatever its typed reason.
	for _, b := range s.blockersFor(id) {
		if !b.Open() {
			continue
		}
		reasons = append(reasons, BlockedReason{
			Kind:      string(b.Reason),
			Detail:    b.Criterion,
			Actor:     b.Actor,
			BlockerID: b.Key.Blocker,
			Truth:     TruthFalse,
			Origin:    b.Held,
		})
	}

	// A successful attempt closes the ATTEMPT, not the obligation. Without an
	// authorised closure the task is awaiting acceptance, and this is the typed
	// queue that exists so it never reads as done.
	if !closed {
		for _, a := range attempts {
			if a.Terminal != nil && a.Terminal.Outcome == model.AttemptSuccess {
				reasons = append(reasons, BlockedReason{
					Kind:   ReasonAwaitingAcceptance,
					Detail: fmt.Sprintf("attempt %s succeeded with no authorised closure", a.Key.Attempt),
					Actor:  rec.Task.NextActor,
					Truth:  TruthFalse,
					Origin: a.Terminal.Origin,
				})
				break
			}
		}
	} else if closure.Outcome == model.ClosureSuccess {
		// Reached only when predicate 1 fell through on witnesses.
		if missing, ok := s.witnessed(rec, closure); !ok {
			detail := "the success closure carries no delivery witness"
			if len(missing) > 0 {
				detail = fmt.Sprintf("the success closure does not witness acceptance criterion %s revision %d",
					missing[0].ID, missing[0].Revision)
			}
			reasons = append(reasons, BlockedReason{
				Kind:   ReasonAwaitingAcceptance,
				Detail: detail,
				Actor:  closure.Authority.Actor,
				Truth:  TruthFalse,
				Origin: closure.Origin,
			})
		}
	}

	// A terminal receipt that declares reconciliation owed keeps the task
	// blocked until an admitted reconciliation clear discharges it. The facet
	// is the writer saying the observed outcome is not settled, and only an
	// admitted event may settle it.
	for _, a := range attempts {
		if a.Terminal == nil || !a.Terminal.ReconciliationOwed {
			continue
		}
		if s.reconciled(id, a.Terminal.Origin) {
			continue
		}
		reasons = append(reasons, BlockedReason{
			Kind:   ReasonReconciliation,
			Detail: fmt.Sprintf("attempt %s terminated %s with reconciliation owed", a.Key.Attempt, a.Terminal.Outcome),
			Actor:  a.Actor,
			Truth:  TruthUnknown,
			Origin: a.Terminal.Origin,
		})
	}

	sort.SliceStable(reasons, func(i, j int) bool {
		if reasons[i].Kind != reasons[j].Kind {
			return reasons[i].Kind < reasons[j].Kind
		}
		if reasons[i].Origin != reasons[j].Origin {
			return reasons[i].Origin.before(reasons[j].Origin)
		}
		return reasons[i].Detail < reasons[j].Detail
	})
	return reasons
}

// reconciled reports whether a reconciliation hold was cleared after the given
// terminal receipt. Anything earlier settled a different question.
func (s *state) reconciled(id Ident, after Origin) bool {
	for _, b := range s.blockersFor(id) {
		if b.Reason != model.BlockerReconciliation || b.Cleared == nil {
			continue
		}
		if after.before(*b.Cleared) {
			return true
		}
	}
	return false
}

// dependencyActor names whoever must act on an unmet dependency. When the
// dependency is not resolvable here the actor is explicitly unknown rather than
// defaulted to the consuming task's own next actor, which would point the queue
// at the wrong person.
func (s *state) dependencyActor(target model.RecordRef) model.Actor {
	if target.Project != s.project {
		return model.Actor{UnknownReason: "the dependency is in another project and resolves at read time"}
	}
	rev, ok := s.current[ident(target)]
	if !ok {
		return model.Actor{UnknownReason: "no admitted revision of the dependency"}
	}
	rec, ok := s.records[RecordKey{Project: target.Project, ID: target.RecordID, Revision: rev}]
	if !ok || rec.Kind != model.Task {
		return model.Actor{UnknownReason: "the dependency names no next actor"}
	}
	return rec.Task.NextActor
}

func waitingActors(reasons []BlockedReason) []model.Actor {
	seen := map[model.Actor]bool{}
	out := []model.Actor{}
	for _, r := range reasons {
		if r.Actor == (model.Actor{}) || seen[r.Actor] {
			continue
		}
		seen[r.Actor] = true
		out = append(out, r.Actor)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].UnknownReason < out[j].UnknownReason
	})
	return out
}
