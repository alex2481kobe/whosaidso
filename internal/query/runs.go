package query

// Run visibility: how long each admitted invocation took and whether its
// recorded input paths stayed inside its task's declared scope.source_paths.
// Durations come only from recorded start and observation times; scope only
// from typed artifact paths. Argv is shown verbatim and never parsed for
// paths, because reading meaning out of a command line is inference.

import (
	"fmt"
	"path"
	"strings"
	"time"

	"datum/internal/model"
	"datum/internal/reduce"
)

type RunView struct {
	Invocation model.ID        `json:"invocation"`
	Task       any             `json:"task"` // model.RecordRef or Unknown
	Attempt    model.ID        `json:"attempt"`
	Instrument model.RecordRef `json:"instrument"`
	Argv       []string        `json:"argv"`
	StartedAt  time.Time       `json:"started_at"`
	Sealed     bool            `json:"sealed"`
	ObservedAt any             `json:"observed_at"` // time.Time or Unknown
	Duration   any             `json:"duration"`    // Duration or Unknown
	Outcome    any             `json:"outcome"`     // model.ProcessOutcome or Unknown
	InputPaths []any           `json:"input_paths"` // string or Unknown per input
	Scope      ScopeCheck      `json:"scope"`
	Origin     reduce.Origin   `json:"origin"`
	SealOrigin *reduce.Origin  `json:"seal_origin,omitempty"`
}

type Duration struct {
	Nanoseconds int64  `json:"nanoseconds"`
	Text        string `json:"text"`
}

// ScopeCheck is TRUE when every recorded input path lies inside the task's
// declared source paths, FALSE when any lies outside, UNKNOWN otherwise.
type ScopeCheck struct {
	WithinTaskScope reduce.Truth `json:"within_task_scope"`
	SourcePaths     []string     `json:"source_paths"`
	Outside         []string     `json:"outside"`
	Reason          string       `json:"reason,omitempty"`
}

func unknown(reason string) Unknown { return Unknown{State: "UNKNOWN", Reason: reason} }

func runView(s reduce.Snapshot, inv reduce.Invocation) RunView {
	task := model.RecordRef{Project: inv.Attempt.Project, RecordID: inv.Attempt.Task}
	v := RunView{Invocation: inv.Key.InvocationID, Attempt: inv.Attempt.Attempt, Instrument: inv.Start.InstrumentRef,
		Argv: nonNil(inv.Start.Argv), StartedAt: inv.Start.StartedAt, Sealed: inv.Seal != nil,
		ObservedAt: unknown("no seal admitted: the end of this run was never observed"),
		Duration:   unknown("no seal admitted: the end of this run was never observed"),
		Outcome:    unknown("no seal admitted: the end of this run was never observed"),
		InputPaths: []any{}, Origin: inv.Started, SealOrigin: inv.Sealed}
	if inv.Seal != nil {
		v.ObservedAt, v.Duration = elapsed(inv.Start.StartedAt, inv.Seal.ObservedAt)
		v.Outcome = unknown("seal recorded no outcome: " + inv.Seal.Outcome.Reason)
		if inv.Seal.Outcome.State == model.Known && inv.Seal.Outcome.Value != nil {
			v.Outcome = *inv.Seal.Outcome.Value
		}
	}
	var sourcePaths []string
	var scopeReason string
	for _, a := range s.Attempts(reduce.Ident{Project: task.Project, ID: task.RecordID}) {
		if a.Key != inv.Attempt {
			continue
		}
		task.Revision = a.TaskRevision
		if rec, ok := s.Record(task); ok && rec.Task != nil {
			sourcePaths = rec.Task.Scope.SourcePaths
		} else {
			scopeReason = "the attempt's task revision is not admitted in this snapshot"
		}
	}
	v.Task = task
	if task.Revision == 0 {
		scopeReason = "no admitted attempt owns this run"
		v.Task = unknown(scopeReason)
	}
	var paths []string
	pathless := false
	for _, ref := range inv.Start.InputRefs {
		found := artifactPaths(ref)
		if len(found) == 0 {
			pathless = true
			v.InputPaths = append(v.InputPaths, unknown("input is pinned by content with no recorded path"))
		}
		for _, p := range found {
			paths = append(paths, p)
			v.InputPaths = append(v.InputPaths, p)
		}
	}
	v.Scope = scopeCheck(sourcePaths, paths, pathless, scopeReason)
	return v
}

func elapsed(start time.Time, observed model.Availability[time.Time]) (any, any) {
	if observed.State != model.Known || observed.Value == nil {
		why := "seal recorded no observation time: " + observed.Reason
		return unknown(why), unknown(why)
	}
	end := *observed.Value
	d := end.Sub(start)
	if d < 0 {
		return end, unknown("recorded observation time precedes the recorded start time")
	}
	return end, Duration{Nanoseconds: int64(d), Text: d.String()}
}

func artifactPaths(ref model.ArtifactRef) []string {
	var out []string
	if ref.Git != nil && ref.Git.Path != "" {
		out = append(out, ref.Git.Path)
	}
	if ref.Content != nil {
		for _, l := range ref.Content.Locators {
			if l.Path != "" {
				out = append(out, l.Path)
			}
		}
	}
	return out
}

func within(p string, scope []string) bool {
	p = path.Clean(p)
	for _, root := range scope {
		root = path.Clean(root)
		if root == "." || p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}
	return false
}

func scopeCheck(sourcePaths, paths []string, pathless bool, reason string) ScopeCheck {
	c := ScopeCheck{WithinTaskScope: reduce.TruthUnknown, SourcePaths: nonNil(sourcePaths), Outside: []string{}}
	switch {
	case reason != "":
		c.Reason = reason
		return c
	case len(sourcePaths) == 0:
		c.Reason = "the task declares no scope.source_paths to compare against"
		return c
	}
	for _, p := range paths {
		if !within(p, sourcePaths) {
			c.Outside = append(c.Outside, p)
		}
	}
	switch {
	case len(c.Outside) > 0:
		c.WithinTaskScope = reduce.TruthFalse
	case len(paths) == 0:
		c.Reason = "the run recorded no input paths"
	case pathless:
		c.Reason = "some inputs have no recorded path"
	default:
		c.WithinTaskScope = reduce.TruthTrue
	}
	return c
}

// runs selects invocations in the snapshot's stable order.
func runs(s reduce.Snapshot, keep func(reduce.Invocation) bool) ([]RunView, []Attention) {
	out, notes := []RunView{}, []Attention{}
	for _, inv := range s.Invocations() {
		if !keep(inv) {
			continue
		}
		v := runView(s, inv)
		out = append(out, v)
		if v.Scope.WithinTaskScope == reduce.TruthFalse {
			notes = append(notes, Attention{Kind: "run-outside-task-scope", Ref: v.Task.(model.RecordRef), Label: "run " + string(v.Invocation),
				Reason: fmt.Sprintf("inputs outside declared source_paths: %s", strings.Join(v.Scope.Outside, ", "))})
		}
	}
	return out, notes
}
