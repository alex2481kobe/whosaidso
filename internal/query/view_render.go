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
	"sort"
)

// RenderViewJSON exports exactly the answer.
func RenderViewJSON(w io.Writer, answer ViewAnswer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(answer)
}

// RenderViewBrief writes the concise reading. --json keeps full detail.
func RenderViewBrief(w io.Writer, answer ViewAnswer) error {
	var encoded bytes.Buffer
	if err := RenderViewJSON(&encoded, answer); err != nil {
		return err
	}
	text, _, err := ViewBriefOf(encoded.Bytes())
	if err != nil {
		return err
	}
	_, err = io.WriteString(w, text)
	return err
}

// ViewBriefOf renders a view's brief from its JSON export and returns the facts
// it shows, each with the path it was read from.
func ViewBriefOf(exported []byte) (string, []BriefFact, error) {
	decoder := json.NewDecoder(bytes.NewReader(exported))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return "", nil, err
	}
	b := &briefWriter{}
	a := cur{v: value}
	briefHeader(b, a, a.at("view"))
	switch a.at("view").text() {
	case "todo":
		briefTodo(b, a)
	case "continue":
		briefContinue(b, a)
	case "show":
		briefShow(b, a)
	case "history":
		briefList(b, 0, "events", a.at("events"), briefEvent)
		if len(a.at("reviews").items()) > 0 {
			briefList(b, 0, "reviews", a.at("reviews"), briefReview)
		}
	}
	b.line(0, "full detail: add --json")
	return b.out.String(), b.facts, nil
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
	pieces := append([]any{"author"}, who(d.at("author", "actor"))...)
	pieces = append(append(pieces, "admitted by"), who(d.at("admitted_by"))...)
	b.line(indent+1, append(pieces, "self-admitted", d.at("self_admitted"))...)
}

func briefTodoTask(b *briefWriter, indent int, t cur) {
	briefDetail(b, indent, t)
	if acc := t.at("accepter"); acc.ok() {
		if _, anyone := acc.v.(string); anyone {
			b.line(indent+1, "accepter", acc)
		} else {
			b.line(indent+1, append([]any{"accepter"}, who(acc)...)...)
		}
	}
	if runs := t.at("runs"); runs.ok() {
		briefList(b, indent+1, "runs", runs, briefRun)
	}
}

func briefTodo(b *briefWriter, a cur) {
	t := a.at("totals")
	b.line(0, "totals: tasks", t.at("tasks"), "in flight", t.at("in_flight"), "awaiting acceptance", t.at("awaiting_acceptance"),
		"blocked", t.at("blocked"), "ready", t.at("ready"), "open decisions", t.at("open_decisions"),
		"intake unreviewed", t.at("intake_unreviewed"), "correction requested", t.at("intake_correction_requested"), "rejected", t.at("intake_rejected"))
	briefList(b, 0, "in flight", a.at("in_flight"), briefTodoTask)
	briefList(b, 0, "awaiting acceptance", a.at("awaiting_acceptance"), briefTodoTask)
	briefList(b, 0, "blocked", a.at("blocked"), briefTodoTask)
	briefList(b, 0, "ready", a.at("ready"), briefTodoTask)
	if o := a.at("omitted", "ready"); o.ok() {
		b.line(0, "limit: ready requested", o.at("requested"), "offered", o.at("offered"), "omitted", o.at("omitted"))
	}
	briefList(b, 0, "open decisions", a.at("open_decisions"), briefDetail)
	briefIntake(b, "intake unreviewed", t.at("intake_unreviewed"), a.at("intake_pending"), "pending")
	briefIntake(b, "correction requested (each packet's author owes a corrected packet)", t.at("intake_correction_requested"), a.at("intake_pending"), "correction-requested")
	if n := t.at("intake_rejected"); n.string() != "0" {
		b.line(0, "rejected intake:", n, "(reviewed, nothing owed; datum history lists the reviews)")
	}
	briefList(b, 0, "attention", a.at("attention"), briefAttention)
}

// briefIntake lists the intake_pending packets with one disposition, under
// the total that counts them.
func briefIntake(b *briefWriter, title string, total, list cur, disposition string) {
	if total.string() == "0" {
		b.line(0, title+": none")
		return
	}
	b.line(0, title+":", total)
	for _, p := range list.items() {
		if p.at("disposition").text() == disposition {
			briefPacket(b, 1, p)
		}
	}
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

func briefContinue(b *briefWriter, a cur) {
	root := a.at("record")
	if !root.ok() {
		return
	}
	b.line(0, "record:")
	briefDetail(b, 1, a.at("records", root.at("record_id").text()+"@"+root.at("revision").string()))
	if p := a.at("progress"); p.ok() {
		if p.at("state").ok() {
			b.line(0, "progress:", p.at("state"), prefix(p.at("reason")))
		} else {
			b.line(0, "progress: witness refs", count(p.at("witness_refs")))
			if p.at("summary").ok() {
				b.line(1, prefix(p.at("summary")))
			}
			if p.at("next_action").ok() {
				b.line(1, "next:", prefix(p.at("next_action")))
			}
		}
	}
	if at := a.at("attempts"); at.ok() {
		briefList(b, 0, "attempts", at, func(b *briefWriter, indent int, v cur) {
			head := append([]any{"attempt", v.at("attempt", "attempt"), "held by"}, who(v.at("holder"))...)
			b.line(indent, append(append(head, "live", v.at("live"), "outcome"), either(v.at("outcome"))...)...)
			b.line(indent+1, append([]any{"next:"}, either(v.at("next_action"))...)...)
		})
	}
	if o := a.at("owed"); o.ok() {
		b.line(0, append([]any{"owed: status", o.at("status"), "next"}, who(o.at("next_actor"))...)...)
		listed := rootReasons(a.at("records", root.at("record_id").text()+"@"+root.at("revision").string()))
		for _, r := range o.at("reasons").items() {
			if listed[reasonKey(r.at("kind").text(), r.at("detail").text())] {
				continue // already printed as a blocked: line under record
			}
			b.line(1, append(append([]any{"waits:", r.at("kind"), "on"}, who(r.at("waiting_actor"))...), "-", prefix(r.at("detail")))...)
		}
		for _, item := range o.at("items").items() {
			target := a.at("records", item.at("target", "record_id").text()+"@"+item.at("target", "revision").string())
			pieces := append([]any{"item", item.at("target", "record_id"), "rev", item.at("target", "revision"), item.at("kind"),
				"satisfied", item.at("satisfied"), "status"}, either(item.at("status"))...)
			if item.at("status").text() == "READY" {
				if last := lastAttemptOutcome(target); last.ok() && last.text() != "success" {
					pieces = append(pieces, "- last attempt", last)
				}
			}
			b.line(1, pieces...)
			if target.at("label").ok() {
				b.line(2, prefix(target.at("label")))
			}
		}
	}
	if r := a.at("runs"); r.ok() {
		briefList(b, 0, "runs", r, briefRun)
	}
	c := a.at("closure")
	b.line(0, "closure: mandatory", count(c.at("mandatory")), "cycles", count(c.at("cycles")))
	briefRefs(b, a, c.at("mandatory"))
	x := a.at("context")
	b.line(0, "context: refs", count(x.at("refs")), "offered", x.at("limit", "offered"), "omitted", x.at("limit", "omitted"))
	briefRefs(b, a, x.at("refs"))
	o := a.at("observed")
	b.line(0, "observed: head", o.at("head", "state"), "dirty", o.at("dirty", "state"), "at", o.at("observed_at", "state"))
	for _, field := range []string{"head", "dirty", "observed_at"} {
		switch v := o.at(field, "value"); {
		case !v.ok():
			b.line(1, field, prefix(o.at(field, "reason")))
		case field == "head":
			b.line(1, "head", v.at("commit"))
		default:
			b.line(1, field, v)
		}
	}
	briefList(b, 0, "attention", a.at("attention"), briefAttention)
}

func briefShow(b *briefWriter, a cur) {
	if s := a.at("summary"); s.ok() {
		b.line(0, "summary: kind", s.at("kind"), "records", s.at("records"), "attention", s.at("attention"))
		for _, kind := range []string{"tasks", "claims", "decisions", "instruments"} {
			counts, _ := s.at(kind).v.(map[string]any)
			keys := make([]string, 0, len(counts))
			for k := range counts {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			if s.at(kind).ok() && len(keys) == 0 {
				b.line(1, kind+": none")
			} else if s.at(kind).ok() {
				pieces := []any{kind + ":"}
				for _, k := range keys {
					pieces = append(pieces, k, s.at(kind, k))
				}
				b.line(1, pieces...)
			}
		}
		if r := s.at("runs"); r.ok() {
			b.line(1, "runs: total", r.at("total"), "sealed", r.at("sealed"), "unsealed", r.at("unsealed"))
		}
		if o := s.at("owed"); o.ok() {
			b.line(1, "owed: in flight", o.at("in_flight"), "awaiting acceptance", o.at("awaiting_acceptance"), "blocked", o.at("blocked"),
				"ready", o.at("ready"), "open decisions", o.at("open_decisions"))
		}
	}
	briefList(b, 0, "attention", a.at("attention"), briefAttention)
	groups := []struct{ kind, title string }{{"TASK", "tasks"}, {"CLAIM", "claims"}, {"DECISION", "decisions"}, {"INSTRUMENT", "instruments"}}
	records := a.at("records").items()
	if len(records) == 0 {
		b.line(0, "records: none")
	}
	shown := 0
	for _, g := range groups {
		first := true
		for _, r := range records {
			if r.at("fact", "kind").text() != g.kind {
				continue
			}
			if first {
				b.line(0, g.title+":")
				first = false
			}
			briefDetail(b, 1, r)
			shown++
		}
	}
	if shown < len(records) {
		b.line(0, "other records:")
		for _, r := range records {
			if _, grouped := briefLabels[r.at("fact", "kind").text()]; !grouped {
				briefDetail(b, 1, r)
			}
		}
	}
	if r := a.at("runs"); r.ok() {
		briefList(b, 0, "runs", r, briefRun)
	}
	if st := a.at("stale"); st.ok() {
		b.line(0, "stale blind spot:", prefix(st.at("blind_spot")))
		briefList(b, 0, "stale claims", st.at("claims"), briefStale)
	}
}

func reasonKey(kind, detail string) string { return kind + "\x00" + detail }

// rootReasons are the reasons the continued record's own detail already
// prints, so the owed section does not print them a second time.
func rootReasons(root cur) map[string]bool {
	seen := map[string]bool{}
	for _, r := range root.at("task", "reasons").items() {
		seen[reasonKey(r.at("kind").text(), r.at("detail").text())] = true
	}
	return seen
}

// lastAttemptOutcome is the terminal outcome of a task detail's latest
// attempt, or nothing when it has no attempt or the latest is still open.
func lastAttemptOutcome(task cur) cur {
	attempts := task.at("task", "attempts").items()
	if len(attempts) == 0 {
		return cur{}
	}
	return attempts[len(attempts)-1].at("terminal", "outcome")
}
