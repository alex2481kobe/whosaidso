package main

// This file holds the `whosaidso template` verb: its bind flags and --set.
// Printing and --capture, which hands the event to capture's own path so the
// gate stays the judge, live in template_capture.go. WhoSaidSo fills only what has one
// computable answer: references and current revisions (template_ledger.go),
// pins (template_pin.go) and, from the final draft, what their bytes or git state (template_derive.go), the project id, the packet author where it is the
// author or attempt holder, the handback verb's false defaults, and a choice
// with one member; which member of a choice the author filled is decided in
// template_choice.go; where a path may add what the tree lacks, in
// template_grow.go. Judgment stays a placeholder, and capture refuses while
// any placeholder remains.

import (
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/evidence"
	"github.com/alex2481kobe/whosaidso/internal/model"
	"github.com/alex2481kobe/whosaidso/internal/store"
)

// templateBinds are the bind flags as given; "" or empty is not given.
type templateBinds struct {
	from, task, claim, criterion, attempt, hold string
	pins, examples, sets                        blobPaths
}

// boundTemplate is one event's tree being filled, and what was filled.
type boundTemplate struct {
	c        *call
	event    model.EventType
	body     any
	notes    []templateNote
	filled   []string        // "PATH: from WHAT", printed on stderr
	putPaths map[string]bool // the paths put filled
	minted   map[string]bool // every id this draft's skeletons minted, appended elements' too
	blobs    []string        // files whose bytes a capture must carry
	author   model.Actor
	bound    bool // a bind flag, --set or --pin was given: list what is left
	// examples maps an output name to the local file holding its example bytes
	examples map[string]string
	project  *store.Project
	state    *store.State
	// pinned are the artifacts --pin resolved, read once the draft is final (template_derive.go)
	pinned    []evidence.ResolvedArtifact
	projectID string // the project id filled from whosaidso.toml, if found
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

// templateAuthorPaths are the actor fields whose one answer is the packet's
// own author: the gate requires it, or (task.start) the attempt holder is
// who hands the attempt back. Filled from --actor or WHOSAIDSO_ACTOR when it is
// known; --set replaces it. An actor who is someone else (a hold's assignee,
// an authority, a waiting or next actor) is never filled.
var templateAuthorPaths = map[model.EventType]string{
	"criterion.fix": "author", "proof.admit": "judgment.actor", "task.takeover": "actor", "task.start": "actor",
}

// templateDefaults are the values the handback verb defaults: a receipt
// states commits_denied or reconciliation_owed only when it is true
// (--set PATH=true), in the verb and the template alike.
var templateDefaults = map[model.EventType][]string{
	"attempt.terminal": {"commits_denied", "reconciliation_owed"},
}

// templateMintRevisions are the revisions of ids the template mints: a new
// id's first revision is 1.
var templateMintRevisions = map[model.EventType][]string{
	"task.create": {"spec.acceptance_criteria[0].revision"}, "criterion.fix": {"revision"},
	"task.amend": {"replacement.acceptance_criteria[0].revision"},
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
	jsonOutput := fs.Bool("json", false, "--capture: print the answer as one JSON object, with the ids the event created")
	return func(c *call) error {
		if len(c.args) != 1 || c.argv != nil {
			return usageError("whosaidso template takes exactly one EVENT-TYPE:\n%s", templateEventList())
		}
		switch {
		case *jsonOutput && !*capture:
			return usageError("whosaidso template: --json needs --capture; a printed template is already JSON")
		case *admitAfter && !*capture:
			return usageError("whosaidso template: --admit needs --capture")
		case *admitAfter && model.Blank(*reason):
			return usageError("whosaidso template --admit needs --reason: the review reason is authored, never defaulted")
		case !*admitAfter && isSet(fs, "reason"):
			return usageError("whosaidso template: --reason belongs to --admit")
		}
		event := model.EventType(c.args[0])
		body, notes, err := buildTemplateTree(event)
		if err != nil {
			return err
		}
		for name, events := range templateBindEvents {
			if isSet(fs, name) && !containsEvent(events, event) {
				return usageError("whosaidso template %s: --%s fills nothing in this event; it binds %s", event, name, eventNames(events))
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
		return t.capture(*admitAfter, *reason, *jsonOutput)
	}
}

// fill applies, in order: what needs no flag, the bind flags, --set, then
// what the final references' pinned bytes state.
func (t *boundTemplate) fill(b templateBinds) error {
	if p, err := store.Discover(t.c.cwd); err == nil {
		t.projectID = string(p.ID)
	}
	if err := t.fillUnflagged(); err != nil {
		return err
	}
	if err := t.bindLedger(b); err != nil {
		return err
	}
	t.examples = map[string]string{}
	for _, e := range b.examples {
		output, file, ok := strings.Cut(e, "=")
		if !ok || output == "" || file == "" {
			return usageError("whosaidso template: --example takes OUTPUT=FILE, got %q", e)
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
			return usageError("whosaidso template: --example %s is pinned nowhere; pin it with --pin NAME=%s", output, output)
		}
	}
	for _, s := range b.sets {
		path, value, ok := strings.Cut(s, "=")
		if !ok {
			return usageError("whosaidso template: --set takes PATH=VALUE, got %q", s)
		}
		v, err := t.setValue(path, value)
		if err != nil {
			return usageError("whosaidso template: --set %s: %v", path, err)
		}
		if _, whole := v.(templateObject); !whole && v != nil && t.refSlot(path) == nil {
			return usageError("whosaidso template %s: --set %s: %s is a reference: use --pin %s=PATH", t.event, path, path, path)
		}
		if v == nil {
			if err := t.omit(path); err != nil {
				return err
			}
			continue
		}
		if err := t.put(path, v, "--set"); err != nil {
			return err
		}
	}
	t.resolveChoices()
	return t.deriveFinal()
}

// templateNonText are the skeleton's placeholders for fields that are not JSON
// strings (numbers, revisions, counts, booleans), spelled by the builder itself.
var templateNonText = func() map[string]bool {
	b := &templateBuilder{}
	out := map[string]bool{}
	for _, v := range []any{json.Number(""), model.Revision(0), 0, uint64(0), false} {
		out[b.walk(reflect.TypeOf(v), nil, reflect.StructField{}).(string)] = true
	}
	return out
}()

// setValue reads a --set VALUE as the event's schema types the field at path.
// A text field takes VALUE as text, decoded only when it is a JSON string
// literal, so a commit such as 79461799e564 or a numeric-looking name stays
// text. Any other field (number, boolean, object, array) takes VALUE as JSON
// when it parses, else as text for capture to refuse. null (nil) omits an
// optional key, whatever the field.
func (t *boundTemplate) setValue(path, value string) (any, error) {
	if t.textField(path) {
		if v, err := templateValue([]byte(value)); err == nil {
			switch v.(type) {
			case nil, string:
				return v, nil
			}
		}
		return value, nil
	}
	if !json.Valid([]byte(value)) {
		return value, nil
	}
	return templateValue([]byte(value))
}

// textField reports whether the schema holds a JSON string at path: the
// event's skeleton there, or for a map key the skeleton cannot name, the
// draft's own node.
func (t *boundTemplate) textField(path string) bool {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return false // put reports the malformed path
	}
	node, ok := t.skeletonAt(steps)
	if !ok {
		node, ok = templateGet(t.body, steps)
	}
	s, isString := node.(string)
	return ok && isString && !templateNonText[s]
}

// fillUnflagged fills what needs no flag: the project id, the packet author,
// the verb's defaults, a choice with one member, and a minted id's first
// revision. An element a path appends is filled by the same code
// (skeletonAt), so it is the same fresh instance element 0 is.
func (t *boundTemplate) fillUnflagged() error {
	if t.minted == nil {
		t.minted = map[string]bool{}
	}
	for _, n := range t.notes {
		if n.Kind == "minted" {
			t.minted[n.ID] = true
		}
	}
	if t.projectID != "" {
		t.replaceAll(projectPlaceholder, t.projectID, "whosaidso.toml")
	}
	if path, ok := templateAuthorPaths[t.event]; ok && !model.Blank(t.author.ID) {
		if err := t.put(path, model.Actor{ID: t.author.ID}, "the packet author (--actor or WHOSAIDSO_ACTOR)"); err != nil {
			return err
		}
	}
	for _, path := range templateDefaults[t.event] {
		if err := t.put(path, false, "false unless you --set it true, as whosaidso handback"); err != nil {
			return err
		}
	}
	t.body = t.onlyChoices(t.body)
	for _, path := range templateMintRevisions[t.event] {
		if err := t.put(path, json.Number("1"), "a minted id starts at revision 1"); err != nil {
			return err
		}
	}
	return nil
}

// omit is --set PATH=null: an optional key is removed; any other is refused,
// since null is never a value.
func (t *boundTemplate) omit(path string) error {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return usageError("whosaidso template: %v", err)
	}
	if !t.optionalPath(path) {
		node, ok := templateGet(t.body, steps)
		if _, known := t.skeletonAt(steps); !ok && !known {
			return usageError("whosaidso template %s: --set %s=null: %s has no field %s", t.event, path, t.event, path)
		}
		how := "fill it"
		switch node.(type) {
		case []any:
			how = fmt.Sprintf("to leave the list empty, --set '%s=[]' where the rules allow none (quoted, since zsh reads [] as a filename pattern)", path)
		case templateObject:
			how = "fill its fields"
		}
		return usageError("whosaidso template %s: --set %s=null omits only an optional key, and %s is required: %s", t.event, path, path, how)
	}
	if t.body, err = templateDelete(t.body, steps, ""); err != nil {
		return usageError("whosaidso template %s: %v", t.event, err)
	}
	t.filled = append(t.filled, path+": omitted (--set null)")
	return nil
}

// put sets path to v, a Go value or a tree node, and notes where it came from.
func (t *boundTemplate) put(path string, v any, from string) error {
	steps, err := parseTemplatePath(path)
	if err != nil {
		return usageError("whosaidso template: %v", err)
	}
	node := v
	switch v.(type) {
	case templateObject, []any, string, json.Number, bool, nil:
	default:
		if node, err = templateTree(v); err != nil {
			return err
		}
	}
	if t.body, err = t.grow(t.body, steps, nil); err != nil {
		return usageError("whosaidso template %s: %v", t.event, err)
	}
	if t.body, err = templateSet(t.body, steps, node, ""); err != nil {
		return usageError("whosaidso template %s: %v", t.event, err)
	}
	t.filled = append(t.filled, path+": "+from)
	if t.putPaths == nil {
		t.putPaths = map[string]bool{}
	}
	t.putPaths[path] = true
	return nil
}

// replaceAll fills every occurrence of one placeholder string.
func (t *boundTemplate) replaceAll(placeholder, value, from string) {
	var count int
	t.body, count = replacePlaceholder(t.body, placeholder, value)
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
