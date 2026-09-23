package query

// Blocks of the brief the views share: lists, actors, records, attention,
// runs, events, reviews and packets. Every value goes through
// briefWriter.line, which records its JSON path; a carried empty list prints
// "none". Each view's layout lives in view_render.go; selection rules stay in
// the view files.

func briefList(b *briefWriter, indent int, title string, list cur, each func(*briefWriter, int, cur)) {
	items := list.items()
	if len(items) == 0 {
		b.line(indent, title+": none")
		return
	}
	b.line(indent, title+":", count(list))
	for _, item := range items {
		each(b, indent+1, item)
	}
}

// who is an actor or its UNKNOWN branch, never a blank.
func who(c cur) []any {
	if c.at("id").ok() {
		return []any{c.at("id")}
	}
	if c.at("unknown_reason").ok() {
		return []any{"UNKNOWN:", prefix(c.at("unknown_reason"))}
	}
	return []any{c.at("state"), prefix(c.at("reason"))}
}

var briefLabels = map[string]string{"TASK": "task", "CLAIM": "claim", "DECISION": "decision", "INSTRUMENT": "instrument"}

// briefRecord is one Record: kind, id, revision, status, next actor, label,
// then why it is where it is (blocked reasons, live holders).
func briefRecord(b *briefWriter, indent int, r cur) {
	f := r.at("fact")
	kind := f.at("kind").text()
	head := []any{f.at("kind"), f.at("key", "id"), "rev", f.at("key", "revision")}
	switch kind {
	case "TASK":
		head = append(append(head, r.at("task", "status"), "next"), who(r.at("task", "expected_next_actor"))...)
	case "CLAIM":
		head = append(head, r.at("claim", "status"), "support", r.at("current_support"))
	case "DECISION":
		head = append(append(head, r.at("decision", "status"), "waiting on"), who(r.at("decision", "spec", "waiting_actor"))...)
	case "INSTRUMENT":
		head = append(head, "support", r.at("current_support"))
	}
	b.line(indent, head...)
	if field, ok := map[string]string{"TASK": "intent", "CLAIM": "assertion", "DECISION": "question", "INSTRUMENT": "question_answered"}[kind]; ok {
		b.line(indent+1, prefix(f.at(briefLabels[kind], field)))
	}
	for _, reason := range r.at("task", "reasons").items() {
		pieces := append([]any{"blocked:", reason.at("kind")}, "waits on")
		pieces = append(pieces, who(reason.at("actor"))...)
		if reason.at("blocker_id").text() != "" {
			pieces = append(pieces, "hold", reason.at("blocker_id"))
		}
		b.line(indent+1, append(pieces, "-", prefix(reason.at("detail")))...)
	}
	// R15.1: who closed the task, and whether they also did the work.
	if c := r.at("task", "closure"); c.ok() {
		closed := append([]any{"closed:", c.at("outcome"), "by"}, who(c.at("closer", "actor"))...)
		b.line(indent+1, append(closed, "self-accepted", c.at("self_accepted"))...)
	}
	for _, h := range r.at("task", "attempt_holders").items() {
		b.line(indent+1, append([]any{"attempt", h.at("attempt", "attempt"), "held by"}, who(h.at("actor"))...)...)
	}
}

func briefAttention(b *briefWriter, indent int, n cur) {
	pieces := []any{n.at("kind"), n.at("ref", "record_id"), "rev", n.at("ref", "revision")}
	if n.at("waiting_actor").ok() {
		pieces = append(append(pieces, "waits on"), who(n.at("waiting_actor"))...)
	}
	b.line(indent, pieces...)
	b.line(indent+1, prefix(n.at("label")))
	b.line(indent+1, prefix(n.at("reason")))
}

func briefRun(b *briefWriter, indent int, v cur) {
	b.line(indent, "run", v.at("invocation"), "sealed", v.at("sealed"), "started", v.at("started_at"), "attempt", v.at("attempt"))
}

func briefEvent(b *briefWriter, indent int, e cur) {
	pieces := []any{"sequence", e.at("origin", "sequence"), "event", e.at("origin", "event_index"), e.at("event", "type"), "command", e.at("command_id")}
	pieces = append(append(pieces, "admitted by"), who(e.at("admitter"))...)
	b.line(indent, append(append(pieces, "author"), who(e.at("author", "actor"))...)...)
}

func briefReview(b *briefWriter, indent int, r cur) {
	b.line(indent, append(append([]any{"review", r.at("key", "command_id"), r.at("outcome"), "by"}, who(r.at("actor"))...),
		append(append([]any{"author"}, who(r.at("author"))...), "self-admitted", r.at("self_admission"))...)...)
	b.line(indent+1, prefix(r.at("reason")))
}

// briefPacket shows a pending packet's disposition first: a reviewed packet
// stays listed with the outcome that reviewed it.
func briefPacket(b *briefWriter, indent int, p cur) {
	b.line(indent, "packet", p.at("command_id"), p.at("disposition"))
	if u := p.at("unavailable"); u.ok() {
		b.line(indent+1, "bytes", u.at("state"), prefix(u.at("reason")))
	} else {
		types := []any{"author"}
		types = append(append(types, who(p.at("packet", "author"))...), "events", count(p.at("packet", "events")))
		for _, e := range p.at("packet", "events").items() {
			types = append(types, e.at("type"))
		}
		b.line(indent+1, types...)
	}
	if r := p.at("review"); r.ok() {
		b.line(indent+1, append([]any{"reviewed", r.at("outcome"), "by"}, who(r.at("actor"))...)...)
		b.line(indent+2, prefix(r.at("reason")))
	}
}
