// Continue, show and source brief sections render record details. Todo and
// read selection belong in their own files.
package query

import "sort"

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
	briefList(b, 0, "amendments", a.at("amendments"), briefAmendment)
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
		pieces := []any{"summary: kind", s.at("kind"), "records", s.at("records")}
		if s.at("sources").ok() {
			pieces = append(pieces, "sources", s.at("sources"))
		}
		b.line(0, append(pieces, "attention", s.at("attention"))...)
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
	if src := a.at("sources"); src.ok() {
		briefList(b, 0, "sources", src, briefSource)
	}
	if st := a.at("stale"); st.ok() {
		b.line(0, "stale blind spot:", prefix(st.at("blind_spot")))
		briefList(b, 0, "stale claims", st.at("claims"), briefStale)
	}
}

// briefSource is one admitted source: its id, speaker and order, its bytes'
// digest and length and how they are pinned, the records it concerns, then
// who wrote and who admitted it.
func briefSource(b *briefWriter, indent int, src cur) {
	in := src.at("intake")
	b.line(indent, append([]any{"SOURCE", in.at("source_id"), "order", in.at("order"), "speaker"}, who(in.at("speaker"))...)...)
	b.line(indent+1, "bytes sha256", in.at("original_digest"), "length", in.at("length"), "pinned", in.at("source_ref", "kind"))
	briefList(b, indent+1, "referents", in.at("referents"), func(b *briefWriter, indent int, r cur) {
		b.line(indent, r.at("record_id"), "rev", r.at("revision"))
	})
	pieces := authorPieces(src.at("author"))
	pieces = append(append(pieces, "admitted by"), who(src.at("admitted_by"))...)
	b.line(indent+1, append(pieces, "self-admitted", src.at("self_admitted"))...)
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
