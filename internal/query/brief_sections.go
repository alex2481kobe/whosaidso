package query

// Section layout of the brief: which blocks each part of an Answer becomes.
// Every value goes through briefWriter.line, which records its JSON path. A
// section the answer does not carry is not printed; a carried empty list
// prints "none". Deep structures (closure, continue, disposal) print their
// counts and point at --json. Selection rules stay in the preset files.

// briefBody prints records, history and reviews, the preset sections with
// attention first, then intake.
func briefBody(b *briefWriter, a cur, command string) {
	primary := map[string]string{"show": "records", "history": "history", "intake pending": "intake"}[command]
	top := func(name string, each func(*briefWriter, int, cur)) {
		if list := a.at(name); len(list.items()) > 0 || name == primary {
			briefList(b, 0, name, list, each)
		}
	}
	top("records", briefRecord)
	top("history", briefEvent)
	top("reviews", briefReview)
	if p := a.at("preset"); p.ok() {
		briefPreset(b, p)
	}
	top("intake", briefPacket) // after attention and the queues it would push down
}

func briefPreset(b *briefWriter, p cur) {
	briefList(b, 0, "attention", p.at("attention"), briefAttention)
	sections := []struct {
		key, title string
		each       func(*briefWriter, int, cur)
	}{
		{"instruments", "instruments", briefInstrument}, {"claims", "claims", briefClaim},
		{"in_flight", "in flight", briefRecord}, {"blocked", "blocked", briefRecord},
		{"awaiting_acceptance", "awaiting acceptance", briefRecord}, {"ready", "ready", briefRecord},
		{"closed", "closed", briefRecord}, {"decisions", "decisions", briefDecision}, {"runs", "runs", briefRun},
	}
	for _, s := range sections {
		if p.at(s.key).ok() {
			briefList(b, 0, s.title, p.at(s.key), s.each)
		}
	}
	if l := p.at("limit"); l.ok() {
		b.line(0, "limit: requested", l.at("requested"), "offered", l.at("offered"), "omitted", l.at("omitted"))
	}
	if c := p.at("closure"); c.ok() {
		briefClosure(b, c)
	}
	if c := p.at("continue"); c.ok() {
		b.line(0, "continue: task", c.at("task", "record_id"), "rev", c.at("task", "revision"), "attempts", count(c.at("attempts")), "runs", count(c.at("runs")))
		b.line(1, prefix(c.at("handoff")))
		briefClosure(b, c.at("closure"))
	}
	if d := p.at("disposal"); d.ok() {
		b.line(0, "disposal: digest", d.at("target", "digest"), "support_loss", count(d.at("support_loss")), "cited_by", count(d.at("cited_by")))
		for _, ref := range d.at("support_loss").items() {
			b.line(1, ref.at("record_id"), "rev", ref.at("revision"))
		}
	}
}

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

func briefClosure(b *briefWriter, c cur) {
	b.line(0, "closure: mandatory", count(c.at("mandatory")), "optional", count(c.at("optional")), "cycles", count(c.at("cycles")))
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

var briefLabels = map[string]string{"TASK": "Task", "CLAIM": "Claim", "DECISION": "Decision", "INSTRUMENT": "Instrument"}

// briefRecord is one Record: kind, id, revision, status, next actor, label,
// then why it is where it is (blocked reasons, live holders).
func briefRecord(b *briefWriter, indent int, r cur) {
	f := r.at("fact")
	kind := f.at("Kind").text()
	head := []any{f.at("Kind"), f.at("Key", "ID"), "rev", f.at("Key", "Revision")}
	switch kind {
	case "TASK":
		head = append(append(head, r.at("task", "status"), "next"), who(r.at("task", "expected_next_actor"))...)
	case "CLAIM":
		head = append(head, r.at("claim", "Status"), "support", r.at("current_support"))
	case "DECISION":
		head = append(append(head, r.at("decision", "Status"), "waiting on"), who(r.at("decision", "Spec", "waiting_actor"))...)
	case "INSTRUMENT":
		head = append(head, "support", r.at("current_support"))
	}
	b.line(indent, head...)
	if field, ok := map[string]string{"TASK": "intent", "CLAIM": "assertion", "DECISION": "question", "INSTRUMENT": "question_answered"}[kind]; ok {
		b.line(indent+1, prefix(f.at(briefLabels[kind], field)))
	}
	for _, reason := range r.at("task", "reasons").items() {
		pieces := append([]any{"blocked:", reason.at("Kind")}, "waits on")
		pieces = append(pieces, who(reason.at("Actor"))...)
		if reason.at("BlockerID").text() != "" {
			pieces = append(pieces, "hold", reason.at("BlockerID"))
		}
		b.line(indent+1, append(pieces, "-", prefix(reason.at("Detail")))...)
	}
	for _, h := range r.at("task", "attempt_holders").items() {
		b.line(indent+1, append([]any{"attempt", h.at("attempt", "Attempt"), "held by"}, who(h.at("actor"))...)...)
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

func briefInstrument(b *briefWriter, indent int, v cur) {
	b.line(indent, "INSTRUMENT", v.at("ref", "record_id"), "rev", v.at("ref", "revision"), "validation", v.at("validation", "state"), "trust", v.at("trust"))
	b.line(indent+1, prefix(v.at("label")))
}

func briefClaim(b *briefWriter, indent int, v cur) {
	b.line(indent, "CLAIM", v.at("ref", "record_id"), "rev", v.at("ref", "revision"), v.at("status"), "support", v.at("current_support"))
	b.line(indent+1, prefix(v.at("label")))
	b.line(indent+1, prefix(v.at("standing")))
}

func briefDecision(b *briefWriter, indent int, v cur) {
	b.line(indent, append([]any{"DECISION", v.at("ref", "record_id"), "rev", v.at("ref", "revision"), v.at("status"), "waiting on"}, who(v.at("waiting_actor"))...)...)
	b.line(indent+1, prefix(v.at("label")))
}

func briefRun(b *briefWriter, indent int, v cur) {
	b.line(indent, "run", v.at("invocation"), "sealed", v.at("sealed"), "started", v.at("started_at"), "attempt", v.at("attempt"))
}

func briefEvent(b *briefWriter, indent int, e cur) {
	pieces := []any{"sequence", e.at("origin", "Sequence"), "event", e.at("origin", "EventIndex"), e.at("event", "type"), "command", e.at("command_id")}
	pieces = append(append(pieces, "admitted by"), who(e.at("admitter"))...)
	b.line(indent, append(append(pieces, "author"), who(e.at("author", "Author"))...)...)
}

func briefReview(b *briefWriter, indent int, r cur) {
	b.line(indent, append(append([]any{"review", r.at("Key", "CommandID"), r.at("Outcome"), "by"}, who(r.at("Actor"))...),
		append(append([]any{"author"}, who(r.at("Author"))...), "self-admitted", r.at("SelfAdmission"))...)...)
	b.line(indent+1, prefix(r.at("Reason")))
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
		b.line(indent+1, append([]any{"reviewed", r.at("Outcome"), "by"}, who(r.at("Actor"))...)...)
		b.line(indent+2, prefix(r.at("Reason")))
	}
}
