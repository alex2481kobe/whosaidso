package query

// Rendering of the four views: JSON (the complete answer) and the concise
// brief. The brief is built from the view's own JSON export through
// briefWriter (brief.go), so every value it prints carries the path it was
// read from. Selection rules live in the view_*.go files; nothing here
// decides what a view holds.

import (
	"bytes"
	"encoding/json"
	"io"
)

// RenderViewJSON exports exactly the answer.
func RenderViewJSON(w io.Writer, answer ViewAnswer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(answer)
}

// RenderViewBrief writes the concise reading. --json keeps full detail.
func RenderViewBrief(w io.Writer, answer ViewAnswer) error {
	return renderViewBrief(w, answer, false)
}

// RenderViewBriefWithIntakeDetails expands todo's verified packet summaries.
func RenderViewBriefWithIntakeDetails(w io.Writer, answer ViewAnswer) error {
	return renderViewBrief(w, answer, true)
}

func renderViewBrief(w io.Writer, answer ViewAnswer, intakeDetails bool) error {
	data, err := json.Marshal(answer)
	if err != nil {
		return err
	}
	text, _, err := decodeViewBrief(data, intakeDetails, true)
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, text)
	return err
}

// ViewBriefOf renders a view's brief from its JSON export and returns the facts
// it shows, each with the path it was read from.
func ViewBriefOf(exported []byte) (string, []BriefFact, error) {
	return viewBriefOf(exported, false)
}

func viewBriefOf(exported []byte, intakeDetails bool) (string, []BriefFact, error) {
	return decodeViewBrief(exported, intakeDetails, false)
}

func decodeViewBrief(exported []byte, intakeDetails, textOnly bool) (string, []BriefFact, error) {
	decoder := json.NewDecoder(bytes.NewReader(exported))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", nil, err
	}
	b := &briefWriter{textOnly: textOnly}
	a := cur{v: value, textOnly: textOnly}
	briefHeader(b, a, a.at("view"))
	switch a.at("view").text() {
	case "todo":
		if a.at("tasks").ok() {
			briefTaskList(b, a)
			break
		}
		briefTodo(b, a, intakeDetails)
	case "continue":
		briefContinue(b, a)
	case "show":
		briefShow(b, a)
	case "history":
		briefList(b, 0, "events", a.at("events"), briefEvent)
		if len(a.at("reviews").items()) > 0 {
			briefList(b, 0, "reviews", a.at("reviews"), briefReview)
		}
	case "packets":
		if header := a.at("header"); header.ok() {
			briefPacketHeader(b, 0, header)
		} else if a.at("result").text() == "KNOWN" {
			briefList(b, 0, "packets", a.at("packets"), briefPacketHeader)
		}
	}
	b.line(0, "full detail: add --json")
	return b.out.String(), b.facts, nil
}

func briefPacketHeader(b *briefWriter, indent int, p cur) {
	parts := []any{"packet", p.at("command_id"), p.at("disposition"), "author"}
	parts = append(parts, who(p.at("author"))...)
	if writer := p.at("writer"); writer.ok() {
		parts = append(parts, "written by")
		parts = append(parts, who(writer)...)
	}
	parts = append(parts, "payload", p.at("payload", "state"))
	if p.at("event_kinds", "state").text() == "known" {
		parts = append(parts, "events", p.at("event_count", "value"))
		for _, kind := range p.at("event_kinds", "value").items() {
			parts = append(parts, kind)
		}
	} else {
		parts = append(parts, prefix(p.at("payload", "reason")))
	}
	b.line(indent, parts...)
}

// either is a value that is a plain string, or an Unknown with its reason.
func either(c cur) []any {
	if _, plain := c.v.(string); plain {
		return []any{prefix(c)}
	}
	return []any{c.at("state"), prefix(c.at("reason"))}
}

// briefDetail is one Detail: kind, id, revision and the kind's status first,
// then its label, then who wrote and who admitted it.
func briefDetail(b *briefWriter, indent int, d cur) {
	switch d.at("fact", "kind").text() {
	case "TASK":
		briefRecord(b, indent, d)
	case "CLAIM":
		c := d.at("claim")
		b.line(indent, "CLAIM", d.at("ref", "record_id"), "rev", d.at("ref", "revision"), c.at("status"), "support", d.at("current_support"))
		b.line(indent+1, prefix(d.at("label")))
		b.line(indent+1, prefix(c.at("standing")))
		for _, why := range d.at("support_unknown_because").items() {
			b.line(indent+1, "support", prefix(why))
		}
	case "DECISION":
		c := d.at("decision")
		b.line(indent, append([]any{"DECISION", d.at("ref", "record_id"), "rev", d.at("ref", "revision"), c.at("status"), "waiting on"}, who(c.at("waiting_actor"))...)...)
		b.line(indent+1, prefix(d.at("label")))
	case "INSTRUMENT":
		c := d.at("instrument")
		b.line(indent, "INSTRUMENT", d.at("ref", "record_id"), "rev", d.at("ref", "revision"), "validation", c.at("validation", "state"), "trust", c.at("trust"))
		b.line(indent+1, prefix(d.at("label")))
		if c.at("validation", "reason").ok() {
			b.line(indent+1, "validation UNKNOWN:", prefix(c.at("validation", "reason")))
		}
		b.line(indent+1, "blind to:", prefix(c.at("blind_to")))
	default:
		b.line(indent, d.at("fact", "kind"), d.at("ref", "record_id"), "rev", d.at("ref", "revision"))
		b.line(indent+1, prefix(d.at("label")))
	}
	pieces := authorPieces(d.at("author"))
	pieces = append(append(pieces, "admitted by"), who(d.at("admitted_by"))...)
	b.line(indent+1, append(pieces, "self-admitted", d.at("self_admitted"))...)
}

func briefRefs(b *briefWriter, a cur, refs cur) {
	for _, n := range refs.items() {
		head := []any{n.at("ref", "record_id"), "rev", n.at("ref", "revision"), "via", n.at("via", 0, "relation")}
		if u := n.at("unresolved"); u.ok() {
			b.line(1, append(head, u.at("state"), prefix(u.at("reason")))...)
			continue
		}
		body := a.at("records", n.at("ref", "record_id").text()+"@"+n.at("ref", "revision").string())
		b.line(1, append(head, body.at("fact", "kind"))...)
		b.line(2, prefix(body.at("label")))
	}
}

func (c cur) string() string {
	if n, ok := c.v.(json.Number); ok {
		return n.String()
	}
	return c.text()
}
