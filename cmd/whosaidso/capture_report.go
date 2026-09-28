package main

// This file holds what a capture reports around the packet itself: the ids its
// events create, as the lines a reader follows and as the list a --json answer
// carries (so a script never digs through notes for a new record's id), and the
// refusal of a packet that still holds template placeholders, naming every one
// at once. Capturing the packet lives in write.go.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/alex2481kobe/whosaidso/internal/model"
)

// createdRecord is one id a captured event creates: which event, its type, the
// path the id sits at, and the id.
type createdRecord struct {
	Event int    `json:"event"`
	Type  string `json:"type"`
	Path  string `json:"path"`
	ID    string `json:"id"`
}

// createdRecords lists the ids the captured events create (template's minted
// paths). A criterion.fix past revision 1 reuses its criterion's id, so it is
// not new.
func createdRecords(events []model.Event) []createdRecord {
	out := []createdRecord{}
	for i, event := range events {
		tree, err := templateValue(event.Data)
		if err != nil {
			continue
		}
		if event.Type == "criterion.fix" {
			if rev, _ := templateGet(tree, []templateStep{{key: "revision", index: -1}}); rev != json.Number("1") {
				continue
			}
		}
		for _, path := range templateMints[event.Type] {
			steps, err := parseTemplatePath(path)
			if err != nil {
				continue
			}
			for _, found := range templateGetAll(tree, steps, "") {
				out = append(out, createdRecord{Event: i, Type: string(event.Type), Path: fmt.Sprint(found[0]), ID: fmt.Sprint(found[1])})
			}
		}
	}
	return out
}

// createdIDs names the created ids for a reader, so the next command can name
// them: "new      claim.assert id = ID".
func createdIDs(events []model.Event) []string {
	var out []string
	for _, r := range createdRecords(events) {
		label := r.Type
		if len(events) > 1 {
			label = fmt.Sprintf("event %d %s", r.Event, r.Type)
		}
		out = append(out, fmt.Sprintf("new      %s %s = %s", label, r.Path, r.ID))
	}
	return out
}

// anyIndex is a list index in a path, so a placeholder under element 3 still
// matches the template's notes, which name element 0.
var anyIndex = regexp.MustCompile(`\[\d+\]`)

// refuseUnfilled refuses a packet that still holds template placeholders and
// names every one at once, where the decoder alone stops at the first. A
// placeholder inside an optional key says so: deleting that key leaves it out.
func refuseUnfilled(events []model.Event) error {
	var lines []string
	for i, event := range events {
		paths := model.Placeholders(event.Data)
		if len(paths) == 0 {
			continue
		}
		var optional []string
		if _, notes, err := buildTemplateTree(event.Type); err == nil {
			for _, n := range notes {
				if n.Kind == "optional" {
					optional = append(optional, templatePath(n.Path))
				}
			}
		}
		for _, p := range paths {
			rel := strings.TrimPrefix(p, "event.data.")
			line := p
			if len(events) > 1 {
				line = fmt.Sprintf("event %d %s: %s", i, event.Type, p)
			}
			if key := optionalAncestor(anyIndex.ReplaceAllString(rel, "[0]"), optional); key != "" {
				line += "  (inside optional " + key + ": delete it to leave it out)"
			}
			lines = append(lines, "  "+line)
		}
	}
	if len(lines) == 0 {
		return nil
	}
	return fmt.Errorf("capture refused: %d template placeholder(s) still unfilled:\n%s\n"+
		"write each value, not the hint; delete an optional key you do not want; a list you need none of is [] where the rules allow it",
		len(lines), strings.Join(lines, "\n"))
}

// optionalAncestor is the outermost optional key path lies in (or is), else
// "": deleting that whole key is what leaves an unfilled block out.
func optionalAncestor(path string, optional []string) string {
	best := ""
	for _, key := range optional {
		if (path == key || strings.HasPrefix(path, key+".") || strings.HasPrefix(path, key+"[")) && (best == "" || len(key) < len(best)) {
			best = key
		}
	}
	return best
}
