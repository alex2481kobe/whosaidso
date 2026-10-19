package main

// Flag-driven authoring translates CLI values into bound templates. Template
// finalization, capture and the admission gate own packet and validity rules.

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

type authoringFlags struct {
	actor            func(*call) model.Actor
	dry, admit, json *bool
	reason           *string
}

func authoringOptions(fs *flag.FlagSet) authoringFlags {
	return authoringFlags{actor: actorFlag(fs), dry: fs.Bool("dry-run", false, "run admission without capturing; print its result"),
		admit: fs.Bool("admit", false, "capture, then admit as the same actor; project self-admission policy applies"),
		json:  jsonFlag(fs), reason: fs.String("reason", "", "authored review `TEXT`; required with --admit and for task close")}
}

func missingFlags(values ...string) error {
	var missing []string
	for i := 0; i < len(values); i += 2 {
		if model.Blank(values[i+1]) {
			missing = append(missing, values[i])
		}
	}
	if len(missing) > 0 {
		return usageError("missing required fields: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (o authoringFlags) required() []string {
	if *o.admit {
		return []string{"--reason", *o.reason}
	}
	return nil
}

func (o authoringFlags) finish(t *boundTemplate) error {
	if *o.dry && *o.admit {
		return usageError("--dry-run and --admit cannot be combined")
	}
	if !*o.dry {
		return t.capture(*o.admit, *o.reason, *o.json)
	}
	data, err := t.captureData()
	if err != nil {
		return err
	}
	events, err := model.DecodeEvents(data)
	if err != nil {
		return err
	}
	p, err := t.c.project()
	if err != nil {
		return err
	}
	a, err := admissionCheck(t.c.ctx, p, events, t.blobs, nil, t.author)
	if err != nil {
		return err
	}
	if err = printResult(t.c.stdout, *o.json, a, a.Scope+"\nresult: "+a.Result+"\n"+a.text); err != nil {
		return err
	}
	if a.Result == "would-refuse" {
		return &exitError{code: 1}
	}
	return nil
}

func authoringTemplate(c *call, event model.EventType, actor model.Actor) (*boundTemplate, error) {
	body, notes, err := buildTemplateTree(event)
	if err != nil {
		return nil, err
	}
	return &boundTemplate{c: c, event: event, body: body, notes: notes, author: actor, bound: true}, nil
}

// jsonSet protects authored text such as "null" or a quoted number from --set's
// typed-value interpretation. All values still go through template filling.
func jsonSet(path string, value any) string {
	data, _ := json.Marshal(value)
	return path + "=" + string(data)
}

func (t *boundTemplate) source(id string) (model.SourceIntake, error) {
	_, s, err := t.ledger()
	if err != nil {
		return model.SourceIntake{}, err
	}
	for _, source := range s.Snapshot().Sources() {
		if string(source.Intake.SourceID) == id {
			return source.Intake, nil
		}
	}
	return model.SourceIntake{}, fmt.Errorf("no admitted source.intake %s; use source add and admit it first", id)
}

func (t *boundTemplate) taskRefs(ids []string) ([]model.RecordRef, error) {
	refs := []model.RecordRef{}
	for _, id := range ids {
		ref, _, err := t.current(model.ID(id), model.Task)
		if err != nil {
			return nil, err
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// requiredList diagnoses an absent list or any blank supplied element as
// missing CLI input; semantic and stateful validation still belongs to the gate.
func requiredList(values []string) string {
	if len(values) == 0 {
		return ""
	}
	for _, value := range values {
		if model.Blank(value) {
			return ""
		}
	}
	return "given"
}
