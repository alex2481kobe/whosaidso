package reduce

// Which events are about a record or source: the rule history selects by. The
// views choose and render rows; the rule for what counts as about an id lives
// only here, so applied and set-aside events are judged the same way.

import "github.com/alex2481kobe/whosaidso/internal/model"

// RunAbout reports whether a run is about id: its criterion is a criterion of
// that claim, or the attempt it ran under is an attempt on that task.
func (s Snapshot) RunAbout(env model.InvocationEnvelope, id Ident) bool {
	if c := env.CriterionRef.Value; c != nil && c.Claim.Project == id.Project && c.Claim.RecordID == id.ID {
		return true
	}
	owner, ok := s.inner().attemptOwner[Ident{Project: id.Project, ID: env.AttemptID}]
	return ok && owner.Task == id.ID
}

// Touches reports whether event e is about record or source id: it creates or
// revises it, names it, or is a run RunAbout counts. Every event naming a hold
// also names its task. A set-aside bundle applied nothing, so no index of the
// snapshot reaches its events; history asks this of each one, and so judges
// them as it judges applied ones.
func (s Snapshot) Touches(e model.TypedEvent, id Ident) bool {
	switch e := e.(type) {
	case *model.SourceIntake:
		if e.SourceID == id.ID {
			return true
		}
	case *model.InvocationStart:
		if s.RunAbout(e.Envelope, id) {
			return true
		}
	case *model.InvocationSeal:
		if s.RunAbout(e.Envelope, id) {
			return true
		}
	}
	for _, n := range eventOwners(id.Project, e) {
		if n.kind == "record" && n.record.ID == id.ID {
			return true
		}
	}
	refs, err := model.SameProjectReferences(e, id.Project)
	if err != nil {
		return false // only an empty project errs, and replay refuses that first
	}
	for _, r := range refs {
		if r.Record != nil && r.Record.RecordID == id.ID {
			return true
		}
	}
	return false
}
