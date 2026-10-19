// Team selection and idle alarms belong here. These rules consume todo facts;
// they never assign work, change claims, or read a machine's actor environment.
package query

import (
	"fmt"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// taskFor owns the two ways a task belongs to an actor: its authored next
// actor and its live holders. An unknown actor never matches a name.
func taskFor(t TodoTask, name string) (named, held bool) {
	if name == "" {
		return false, false
	}
	named = t.Fact.Task != nil && t.Fact.Task.NextActor.ID == name
	for _, holder := range t.Task.AttemptHolders {
		held = held || actorIs(holder.Actor, name)
	}
	return named, held
}

func actorIs(value any, name string) bool {
	a, ok := value.(model.Actor)
	return ok && name != "" && a.ID == name
}

func idleActors(a *TodoAnswer, team []string) []Attention {
	notes := []Attention{}
	for _, name := range team {
		busy := false
		for _, task := range a.InFlight {
			_, held := taskFor(task, name)
			busy = busy || held
		}
		for _, task := range a.Ready {
			named, _ := taskFor(task, name)
			busy = busy || named
		}
		if busy {
			continue
		}
		note := Attention{Kind: "actor-idle", Label: name + ": no live claim or READY task", WaitingActor: model.Actor{ID: name},
			Reason: "no BLOCKED task names this actor"}
		var nearest *TodoTask
		for _, section := range [][]TodoTask{a.Blocked, a.AwaitingAcceptance} {
			for _, task := range section {
				named, _ := taskFor(task, name)
				if named && (nearest == nil || len(task.Task.Reasons) < len(nearest.Task.Reasons) ||
					len(task.Task.Reasons) == len(nearest.Task.Reasons) && task.Ref.RecordID < nearest.Ref.RecordID) {
					copy := task
					nearest = &copy
				}
			}
		}
		if nearest != nil {
			note.Ref = nearest.Ref
			parts := []string{}
			for _, reason := range nearest.Task.Reasons {
				parts = append(parts, fmt.Sprintf("%s: %s (waiting on %s)", reason.Kind, reason.Detail, actorName(reason.Actor)))
			}
			note.Reason = "nearest BLOCKED task " + string(nearest.Ref.RecordID) + ": " + strings.Join(parts, "; ")
		}
		notes = append(notes, note)
	}
	return notes
}

func filterTodo(a *TodoAnswer, name string, limit int) {
	a.ActorFilter = name
	a.FilterNote = "totals are project-wide; sections are filtered by actor"
	matched := map[model.ID]bool{}
	for _, section := range []*[]TodoTask{&a.InFlight, &a.AwaitingAcceptance, &a.Blocked, &a.Ready} {
		kept := []TodoTask{}
		for _, task := range *section {
			named, held := taskFor(task, name)
			if named || held {
				kept = append(kept, task)
				matched[task.Ref.RecordID] = true
			}
		}
		*section = kept
	}
	notes := []Attention{}
	for _, note := range a.Attention {
		if actorIs(note.WaitingActor, name) || note.Kind != "actor-idle" && matched[note.Ref.RecordID] {
			notes = append(notes, note)
		}
	}
	a.Attention = notes
	decisions := []Detail{}
	for _, decision := range a.OpenDecisions {
		if actorIs(decision.Decision.WaitingActor, name) {
			decisions = append(decisions, decision)
		}
	}
	a.OpenDecisions = decisions
	packets := []Packet{}
	for _, packet := range a.PacketsNotAccepted {
		if packet.Packet != nil && packet.Packet.Author.ID == name {
			packets = append(packets, packet)
		}
	}
	a.PacketsNotAccepted = packets
	a.SetAside = nil
	if len(matched) == 0 && len(notes) == 0 && len(decisions) == 0 && len(packets) == 0 {
		a.FilterNote += "; no work or attention found for " + name
	}
	report := LimitReport{Requested: "none", Offered: len(a.Ready)}
	if limit > 0 {
		report.Requested = limit
		if len(a.Ready) > limit {
			report.Omitted, a.Ready = len(a.Ready)-limit, a.Ready[:limit]
		}
	}
	a.Omitted["ready"] = report
}
