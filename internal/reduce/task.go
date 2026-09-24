package reduce

import (
	"sort"

	"whosaidso/internal/model"
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
		c.Closer = s.eventAuthor(c.Origin)
		c.SelfAccepted = s.selfAccepted(c.Closer.Author, p.Attempts)
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
