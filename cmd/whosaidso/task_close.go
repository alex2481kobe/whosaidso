package main

// Task closure flags bind criterion witnesses in the admitted task's order.
// The template owns revision binding; the gate owns closure effectiveness.

import (
	"flag"
	"fmt"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

func taskCloseVerb(fs *flag.FlagSet) func(*call) error {
	o := authoringOptions(fs)
	outcome := fs.String("outcome", "", "authored closure `OUTCOME`: success, cancelled, withdrawn or waived")
	authority := fs.String("authority", "", "admitted source.intake `ID`; whole source, with explicit scope flags")
	applies := fs.String("applies-when", "", "authority scope applicability `TEXT` (required with --authority)")
	limits := fs.String("limitations", "", "authority scope limits `TEXT` (required with --authority)")
	var witnesses blobPaths
	fs.Var(&witnesses, "witness", "witness `PATH[@REV][#POINTER]`, one per criterion in task order; also a delivery witness (repeatable)")
	return func(c *call) error {
		task := ""
		if len(c.args) == 1 {
			task = c.args[0]
		}
		required := []string{"TASK", task, "--outcome", *outcome, "--reason", *o.reason}
		if isSet(fs, "authority") {
			required = append(required, "--authority", *authority, "--applies-when", *applies, "--limitations", *limits)
		}
		if err := missingFlags(required...); err != nil {
			return err
		}
		if c.argv != nil || len(c.args) != 1 {
			return usageError("task close takes one TASK")
		}
		t, err := authoringTemplate(c, "task.close", o.actor(c))
		if err != nil {
			return err
		}
		ref, _, err := t.current(model.ID(task), model.Task)
		if err != nil {
			return err
		}
		b := templateBinds{task: task, sets: blobPaths{jsonSet("outcome", *outcome), "delivery_witness_refs=[]"}}
		if len(witnesses) == 0 {
			b.sets = append(b.sets, "acceptance_witness_refs=[]")
		} else {
			// Keep the criterion references that bindTask supplies. A missing or extra
			// witness is refused by template finalization or admission, never invented.
			for i, w := range witnesses {
				b.pins = append(b.pins, fmt.Sprintf("acceptance_witness_refs[%d].witness_ref=%s", i, w), fmt.Sprintf("delivery_witness_refs[%d]=%s", i, w))
			}
			b.sets = b.sets[:1]
		}
		if isSet(fs, "authority") {
			s, err := t.source(*authority)
			if err != nil {
				return err
			}
			a := model.Authority{Actor: s.Speaker, SourceRef: s.SourceRef, Selector: model.Selector{Kind: "whole"}, Scope: model.Scope{SourcePaths: []string{}, ContextRefs: []model.RecordRef{ref}, AppliesWhen: *applies, Limitations: *limits}}
			b.sets = append(b.sets, jsonSet("authority", a))
		}
		if err := t.fill(b); err != nil {
			return err
		}
		return o.finish(t)
	}
}
