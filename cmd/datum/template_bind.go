package main

// This file holds the `datum template` verb: its bind flags, --set, and
// --capture, which hands the filled event to capture's own path (captureCLI,
// captureAndAdmit) so the gate stays the judge. Datum fills only what has one
// computable answer: references and current revisions (template_ledger.go),
// pins (template_pin.go), the project id, the packet author where the gate
// requires it, and a choice with one member. Judgment stays a placeholder,
// and capture refuses while any placeholder remains.

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strings"

	"datum/internal/model"
	"datum/internal/store"
)

// templateBinds are the bind flags as given; "" or empty is not given.
type templateBinds struct {
	from, task, claim, criterion, attempt, hold string
	pins, examples, sets                        blobPaths
}

// boundTemplate is one event's tree being filled, and what was filled.
type boundTemplate struct {
	c      *call
	event  model.EventType
	body   any
	notes  []templateNote
	filled []string // "PATH: from WHAT", printed on stderr
	blobs  []string // files whose bytes a capture must carry
	author model.Actor
	bound  bool // a bind flag, --set or --pin was given: list what is left
	// examples maps an output name to the local file holding its example bytes
	examples map[string]string
	project  *store.Project
	state    *store.State
}

// templateBindEvents says which events each bind flag fills. A flag given
// for any other event would fill nothing, so it is a usage error.
var templateBindEvents = map[string][]model.EventType{
	"from":      {"task.amend", "claim.revise", "decision.revise", "instrument.revise"},
	"task":      {"task.start", "task.close", "blocker.hold", "blocker.clear"},
	"hold":      {"blocker.clear"},
	"attempt":   {"task.takeover", "attempt.terminal"},
	"claim":     {"criterion.fix", "proof.admit"},
	"criterion": {"criterion.fix", "proof.admit"},
}

// templateAuthorPaths are the actor fields the gate requires to be the
// packet's own author: filled from --actor or DATUM_ACTOR when it is known.
var templateAuthorPaths = map[model.EventType]string{
	"task.create": "provenance.author", "task.amend": "provenance.author", "claim.assert": "provenance.author",
	"claim.revise": "provenance.author", "decision.open": "provenance.author", "decision.revise": "provenance.author",
	"instrument.declare": "provenance.author", "instrument.revise": "provenance.author",
	"criterion.fix": "author", "proof.admit": "judgment.actor", "task.takeover": "actor",
}

// templateMintRevisions are the revisions of ids the template mints: a new
// id's first revision is 1.
var templateMintRevisions = map[model.EventType][]string{
	"task.create": {"spec.acceptance_criteria[0].revision"}, "criterion.fix": {"revision"},
}

// templateVerb prints one event's skeleton, filled as far as the bind flags
// compute, on stdout and its notes on stderr; with --capture it captures it.
func templateVerb(fs *flag.FlagSet) func(*call) error {
	var b templateBinds
	fs.StringVar(&b.from, "from", "", "amend or revise: the record `ID` whose current spec is copied")
	fs.StringVar(&b.task, "task", "", "the task `ID`: its reference at the current revision")
	fs.StringVar(&b.claim, "claim", "", "the claim `ID`: its reference at the current revision")
	fs.StringVar(&b.criterion, "criterion", "", "the criterion `ID`: its current revision (or the next, for criterion.fix)")
	fs.StringVar(&b.attempt, "attempt", "", "the attempt `ID`: it and its task's reference")
	fs.StringVar(&b.hold, "hold", "", "blocker.clear: the open hold's `ID`; its task is found")
	fs.Var(&b.examples, "example", "a run output's example bytes: `OUTPUT=FILE`; --pin NAME=OUTPUT pins them under that output name (repeatable)")
	fs.Var(&b.pins, "pin", "pin real bytes at a reference field: `NAME=PATH[@REV][#POINTER]`, content or git (repeatable)")
	fs.Var(&b.sets, "set", "fill one field: `PATH=VALUE`, VALUE as JSON when it parses, else text (repeatable)")
	actor := actorFlag(fs)
	capture := fs.Bool("capture", false, "capture the filled event; refused while any placeholder remains")
	admitAfter := fs.Bool("admit", false, "--capture: then admit the packet as accepted under the same actor")
	reason := fs.String("reason", "", "--admit: the review reason `TEXT` (required with --admit)")
	return func(c *call) error {
		if len(c.args) != 1 || c.argv != nil {
			return usageError("datum template takes exactly one EVENT-TYPE:\n%s", templateEventList())
		}
		switch {
		case *admitAfter && !*capture:
			return usageError("datum template: --admit needs --capture")
		case *admitAfter && model.Blank(*reason):
			return usageError("datum template --admit needs --reason: the review reason is authored, never defaulted")
		case !*admitAfter && isSet(fs, "reason"):
			return usageError("datum template: --reason belongs to --admit")
		}
		event := model.EventType(c.args[0])
		body, notes, err := buildTemplateTree(event)
		if err != nil {
			return err
		}
		for name, events := range templateBindEvents {
			if isSet(fs, name) && !containsEvent(events, event) {
				return usageError("datum template %s: --%s fills nothing in this event; it binds %s", event, name, eventNames(events))
			}
		}
		t := &boundTemplate{c: c, event: event, body: body, notes: notes, author: actor(c)}
		fs.Visit(func(f *flag.Flag) {
			t.bound = t.bound || templateBindEvents[f.Name] != nil || f.Name == "set" || f.Name == "pin"
		})
		if err := t.fill(b); err != nil {
			return err
		}
		if !*capture {
			return t.print()
		}
		return t.capture(*admitAfter, *reason)
	}
}

// fill applies, in order: what needs no flag, the bind flags, then --set.
func (t *boundTemplate) fill(b templateBinds) error {
	if p, err := store.Discover(t.c.cwd); err == nil {
		t.replaceAll("<project: the id declared in datum.toml>", string(p.ID), "datum.toml")
	}
	if path, ok := templateAuthorPaths[t.event]; ok && !model.Blank(t.author.ID) {
		if err := t.put(path, model.Actor{ID: t.author.ID}, "the packet author (--actor or DATUM_ACTOR)"); err != nil {
			return err
		}
	}
	t.body = t.onlyChoices(t.body)
	for _, path := range templateMintRevisions[t.event] {
		if err := t.put(path, json.Number("1"), "a minted id starts at revision 1"); err != nil {
			return err
		}
	}
	if err := t.bindLedger(b); err != nil {
		return err
	}
	t.examples = map[string]string{}
	for _, e := range b.examples {
		output, file, ok := strings.Cut(e, "=")
		if !ok || output == "" || file == "" {
			return usageError("datum template: --example takes OUTPUT=FILE, got %q", e)
		}
		t.examples[output] = file
	}
	pinned := map[string]bool{}
	for _, p := range b.pins {
		if err := t.pin(p); err != nil {
			return err
		}
		_, target, _ := strings.Cut(p, "=")
		target, _, _ = strings.Cut(target, "#")
		pinned[target] = true
	}
	for output := range t.examples {
		if !pinned[output] {
			return usageError("datum template: --example %s is pinned nowhere; pin it with --pin NAME=%s", output, output)
		}
	}
	for _, s := range b.sets {
		path, value, ok := strings.Cut(s, "=")
		if !ok {
			return usageError("datum template: --set takes PATH=VALUE, got %q", s)
		}
		var v any = value
		if json.Valid([]byte(value)) {
			var err error
			if v, err = templateValue([]byte(value)); err != nil {
				return usageError("datum template: --set %s: %v", path, err)
			}
		}
		if err := t.put(path, v, "--set"); err != nil {
			return err
		}
	}
	return nil
}

// put sets path to v, a Go value or a tree node, and notes where it came from.
func (t *boundTemplate) put(path string, v any, from string) error {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return usageError("datum template: %v", err)
	}
	node := v
	switch v.(type) {
	case templateObject, []any, string, json.Number, bool, nil:
	default:
		if node, err = templateTree(v); err != nil {
			return err
		}
	}
	if t.body, err = templateSet(t.body, steps, node, ""); err != nil {
		return usageError("datum template %s: %v", t.event, err)
	}
	t.filled = append(t.filled, path+": "+from)
	return nil
}

// replaceAll fills every occurrence of one placeholder string.
func (t *boundTemplate) replaceAll(placeholder, value, from string) {
	count := 0
	var walk func(node any) any
	walk = func(node any) any {
		switch n := node.(type) {
		case templateObject:
			for i := range n {
				n[i].value = walk(n[i].value)
			}
		case []any:
			for i := range n {
				n[i] = walk(n[i])
			}
		case string:
			if n == placeholder {
				count++
				return value
			}
		}
		return node
	}
	t.body = walk(t.body)
	if count > 0 {
		t.filled = append(t.filled, fmt.Sprintf("every %s (%d): %s", placeholder, count, from))
	}
}

// onlyChoices fills a choice with one member: it has one correct answer.
func (t *boundTemplate) onlyChoices(node any) any {
	switch n := node.(type) {
	case templateObject:
		for i := range n {
			n[i].value = t.onlyChoices(n[i].value)
		}
	case []any:
		for i := range n {
			n[i] = t.onlyChoices(n[i])
		}
	case string:
		if member, ok := strings.CutPrefix(n, "<one of: "); ok && !strings.Contains(member, " | ") {
			return strings.TrimSuffix(member, ">")
		}
	}
	return node
}

// print writes the event on stdout, and on stderr the notes, what was filled
// and every placeholder still left to the author.
func (t *boundTemplate) print() error {
	data, err := renderTemplate(t.event, t.body)
	if err != nil {
		return err
	}
	if _, err := t.c.stdout.Write(data); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString(renderTemplateNotes(t.event, t.notes))
	for _, f := range t.filled {
		fmt.Fprintf(&b, "filled   %s\n", f)
	}
	left := templatePlaceholders(t.body, "")
	if t.bound {
		for _, path := range left {
			fmt.Fprintf(&b, "yours    %s\n", path)
		}
	}
	fmt.Fprintf(&b, "left     %d placeholder(s) for you to fill; --capture refuses while any remains\n", len(left))
	for _, blob := range t.blobs {
		fmt.Fprintf(&b, "blob     capture it with --blob %s\n", blob)
	}
	_, err = io.WriteString(t.c.stderr, b.String())
	return err
}

// capture drops optional keys nobody filled, refuses while a placeholder
// remains, then captures (and with --admit admits) through capture's path.
func (t *boundTemplate) capture(admit bool, reason string) error {
	for _, n := range t.notes {
		if n.Kind == "optional" {
			t.body = templateDropUnfilled(t.body, n.Path)
		}
	}
	if left := templatePlaceholders(t.body, ""); len(left) > 0 {
		return fmt.Errorf("template %s: capture refused: %d placeholder(s) unfilled: %s; fill them with --set PATH=VALUE, or print the template and edit it",
			t.event, len(left), strings.Join(left, ", "))
	}
	data, err := renderTemplate(t.event, t.body)
	if err != nil {
		return err
	}
	open := t.c.checkout
	if admit {
		open = t.c.project
	}
	project, err := open()
	if err != nil {
		return err
	}
	ref, count, err := captureCLI(t.c.ctx, project, "", t.author, "-", t.blobs, bytes.NewReader(data))
	if err != nil {
		return err
	}
	if admit {
		return captureAndAdmit(t.c.ctx, project, t.c.stdout, false, ref, count, "", t.author, reason)
	}
	return printResult(t.c.stdout, false, ref, captureAck(ref, count))
}

func containsEvent(events []model.EventType, e model.EventType) bool {
	for _, x := range events {
		if x == e {
			return true
		}
	}
	return false
}

func eventNames(events []model.EventType) string {
	parts := make([]string, len(events))
	for i, e := range events {
		parts[i] = string(e)
	}
	return strings.Join(parts, ", ")
}
