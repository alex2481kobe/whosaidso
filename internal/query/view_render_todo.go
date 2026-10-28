// Todo brief sections render selected work and intake. Selection and other
// view layouts do not belong here.
package query

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
	for _, c := range t.at("claims").items() {
		line := []any{"claim", c.at("attempt_id"), "claimed", c.at("claimed_at"), "last activity", c.at("last_activity_at")}
		if stale, _ := c.at("stale").v.(bool); stale {
			line = append(line, "STALE")
		}
		b.line(indent+1, line...)
	}
}

func briefTodo(b *briefWriter, a cur, intakeDetails bool) {
	if a.at("actor_filter").ok() {
		b.line(0, "actor:", a.at("actor_filter"), "-", a.at("filter_note"))
	}
	t := a.at("totals")
	b.line(0, "totals: tasks", t.at("tasks"), "in flight", t.at("in_flight"), "awaiting acceptance", t.at("awaiting_acceptance"),
		"blocked", t.at("blocked"), "ready", t.at("ready"), "open decisions", t.at("open_decisions"),
		"intake unreviewed", t.at("intake_unreviewed"), "correction requested", t.at("intake_correction_requested"), "rejected", t.at("intake_rejected"))
	if n := t.at("stale_claims"); n.ok() {
		b.line(0, "stale claims:", n, "(takeover candidates; see attention)")
	}
	if aside := a.at("set_aside"); aside.ok() {
		briefList(b, 0, "set aside after a merge (kept in the ledger, not applied)", aside, briefSetAside)
	}
	if n := t.at("set_aside_resolved"); n.ok() {
		b.line(0, "set aside and resolved:", n)
	}
	briefList(b, 0, "in flight", a.at("in_flight"), briefTodoTask)
	briefList(b, 0, "awaiting acceptance", a.at("awaiting_acceptance"), briefTodoTask)
	briefList(b, 0, "blocked", a.at("blocked"), briefTodoTask)
	briefList(b, 0, "ready", a.at("ready"), briefTodoTask)
	if o := a.at("omitted", "ready"); o.ok() {
		b.line(0, "limit: ready requested", o.at("requested"), "offered", o.at("offered"), "omitted", o.at("omitted"))
	}
	briefList(b, 0, "open decisions", a.at("open_decisions"), briefDetail)
	briefIntake(b, "intake unreviewed", t.at("intake_unreviewed"), a.at("packets_not_accepted"), "pending", intakeDetails)
	briefIntake(b, "correction requested (whether a corrected packet answered it is not recorded)", t.at("intake_correction_requested"), a.at("packets_not_accepted"), "correction-requested", intakeDetails)
	if n := t.at("intake_elsewhere"); n.ok() {
		b.line(0, "intake captured in other checkouts (admit each packet in one checkout only, normally where it was captured):", n)
		for _, p := range a.at("packets_not_accepted").items() {
			if p.at("disposition").text() == "captured-elsewhere" {
				briefTodoPacket(b, 1, p, intakeDetails)
				b.line(2, "captured in", p.at("captured_in"))
			}
		}
	}
	if n := t.at("intake_set_aside"); n.ok() {
		b.line(0, "intake in set-aside bundles (admitted on a branch, then set aside; never admit again):", n)
	}
	if n := t.at("intake_rejected"); n.string() != "0" {
		b.line(0, "rejected intake:", n, "(reviewed, nothing owed; whosaidso history lists the reviews)")
	}
	briefList(b, 0, "attention", a.at("attention"), briefAttention)
}

// briefSetAside is one set-aside bundle: which, the fault, what to do.
func briefSetAside(b *briefWriter, indent int, v cur) {
	b.line(indent, "sequence", v.at("sequence"), "command", v.at("command_id"), v.at("code"))
	b.line(indent+1, prefix(v.at("reason")))
	b.line(indent+1, v.at("next"))
	b.line(indent+1, "resolve:", v.at("resolve"))
}

// briefIntake lists the packets_not_accepted packets with one disposition, under
// the total that counts them.
func briefIntake(b *briefWriter, title string, total, list cur, disposition string, details bool) {
	if total.string() == "0" {
		b.line(0, title+": none")
		return
	}
	b.line(0, title+":", total)
	for _, p := range list.items() {
		if p.at("disposition").text() == disposition {
			briefTodoPacket(b, 1, p, details)
		}
	}
}
