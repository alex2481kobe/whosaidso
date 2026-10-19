package main

// Task creation and amendment flags fill the existing task templates. Ledger
// references are bound at their current revisions; admission decides validity.

import (
	"flag"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func taskAuthoringVerb(amend bool) func(*flag.FlagSet) func(*call) error {
	return func(fs *flag.FlagSet) func(*call) error {
		o := authoringOptions(fs)
		subject := fs.String("subject", "", "task subject `TEXT`")
		intent := fs.String("intent", "", "task intent `TEXT`")
		next := fs.String("for", "", "next actor `ACTOR`")
		applies := fs.String("applies-when", "", "authored scope applicability `TEXT` (required for new)")
		limits := fs.String("limitations", "", "authored scope limits `TEXT` (required for new)")
		source := fs.String("source", "", "admitted source.intake `ID` for provenance")
		pin := fs.String("pin", "", "provenance bytes at `PATH[@REV][#POINTER]`")
		var criteria, goals, after blobPaths
		fs.Var(&criteria, "criterion", "acceptance criterion `TEXT` (repeatable; replaces the list on amend)")
		fs.Var(&goals, "non-goal", "explicit non-goal `TEXT` (repeatable; replaces the list on amend)")
		fs.Var(&after, "after", "prerequisite `TASK` at its current revision; success required, waiver forbidden (repeatable; replaces the list on amend)")
		return func(c *call) error {
			required := o.required()
			event, prefix := model.EventType("task.create"), "spec."
			var b templateBinds
			if amend {
				event, prefix = "task.amend", "replacement."
				if len(c.args) == 1 {
					b.from = c.args[0]
				}
				required = append(required, "TASK", b.from)
			} else {
				required = append(required, "--subject", *subject, "--intent", *intent, "--for", *next, "--criterion", requiredList(criteria), "--non-goal", requiredList(goals), "--applies-when", *applies, "--limitations", *limits)
			}
			if amend {
				for _, f := range []struct{ name, value string }{{"subject", *subject}, {"intent", *intent}, {"for", *next}, {"applies-when", *applies}, {"limitations", *limits}, {"criterion", requiredList(criteria)}, {"non-goal", requiredList(goals)}} {
					if isSet(fs, f.name) {
						required = append(required, "--"+f.name, f.value)
					}
				}
			}
			for _, f := range []struct{ name, value string }{{"source", *source}, {"pin", *pin}, {"after", requiredList(after)}} {
				if isSet(fs, f.name) {
					required = append(required, "--"+f.name, f.value)
				}
			}
			if err := missingFlags(required...); err != nil {
				return err
			}
			if c.argv != nil || amend && len(c.args) != 1 || !amend && len(c.args) != 0 {
				return usageError("task new takes no positional arguments; task amend takes one TASK")
			}
			t, err := authoringTemplate(c, event, o.actor(c))
			if err != nil {
				return err
			}
			b.sets = append(b.sets, "provenance.source_refs=[]")
			if !amend {
				for _, path := range []string{"scope.source_paths", "scope.context_refs", "context_refs", "constraint_refs", "prerequisites"} {
					b.sets = append(b.sets, prefix+path+"=[]")
				}
			}
			for _, f := range []struct{ flag, path, value string }{{"subject", "subject", *subject}, {"intent", "intent", *intent}, {"applies-when", "scope.applies_when", *applies}, {"limitations", "scope.limitations", *limits}} {
				if !amend || isSet(fs, f.flag) {
					b.sets = append(b.sets, jsonSet(prefix+f.path, f.value))
				}
			}
			if !amend || isSet(fs, "for") {
				b.sets = append(b.sets, jsonSet(prefix+"next_actor", model.Actor{ID: *next}))
			}
			if !amend || isSet(fs, "non-goal") {
				b.sets = append(b.sets, jsonSet(prefix+"non_goals", goals))
			}
			// Clear the copied criteria before filling a replacement list: template
			// growth mints each new identity and its revision, including element zero.
			if isSet(fs, "criterion") {
				b.sets = append(b.sets, prefix+"acceptance_criteria=[]")
				for i, c := range criteria {
					b.sets = append(b.sets, jsonSet(fmt.Sprintf("%sacceptance_criteria[%d].criterion", prefix, i), c))
				}
			}
			if isSet(fs, "after") {
				refs, err := t.taskRefs(after)
				if err != nil {
					return err
				}
				prereqs := []model.Prerequisite{}
				for _, ref := range refs {
					prereqs = append(prereqs, model.Prerequisite{Kind: "task-success", Target: ref, WaiverPolicy: "forbid"})
				}
				b.sets = append(b.sets, jsonSet(prefix+"prerequisites", prereqs))
			}
			provenance := []model.ArtifactRef{}
			if isSet(fs, "source") {
				source, err := t.source(*source)
				if err != nil {
					return err
				}
				provenance = append(provenance, source.SourceRef)
			}
			if isSet(fs, "pin") {
				// Pins precede sets. Seed the list before filling so the pin
				// appends without a later set overwriting either reference.
				b.sets = b.sets[1:]
				if len(provenance) > 0 {
					if err := t.put("provenance.source_refs", provenance, "--source"); err != nil {
						return err
					}
				}
				b.pins = append(b.pins, fmt.Sprintf("provenance.source_refs[%d]=%s", len(provenance), *pin))
			} else {
				b.sets[0] = jsonSet("provenance.source_refs", provenance)
			}
			if err := t.fill(b); err != nil {
				return err
			}
			return o.finish(t)
		}
	}
}
