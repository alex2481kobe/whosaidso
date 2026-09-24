// Home, "What's owed": one card per section of the todo view, in its order,
// each counted by todo's own totals. An empty section reads "none"; nothing
// is hidden. The list of every project lives in ui-history.js.
import { el, mono, link, art, actorName, since, times } from "./ui.js";

// card is one tinted section: dot, title, count, a one-line meaning, rows.
export function card(tone, title, meaning, count, rows) {
  const body = rows.length ? rows : [el("p", "none", "none")];
  return el("section", "owed-card tone-" + tone,
    el("header", "owed-head", el("span", "dot"), el("h2", "", title), el("span", "count", String(count)), el("p", "meaning", meaning)),
    el("div", "owed-body", body));
}

// row is one owed item: id and label on the left, then who, then a note.
// why, when given, is the reasons list under the row, one per line.
function row(id, label, who, note, noteTone = "", why = []) {
  return el("div", "owed-row",
    el("div", "owed-what", id ? link(id, id, "mono link plain") : null, el("span", "owed-label", label)),
    el("span", "owed-who", who),
    el("span", "owed-note " + noteTone, note),
    why.length ? el("div", "owed-why", why.map((w) => el("div", "", w))) : null);
}

function holder(task) {
  const holders = task.task.attempt_holders || [];
  return holders.length ? holders.map((h) => actorName(h.actor)).join(", ") : "—";
}

function liveSince(task, at) {
  const live = (task.task.attempts || []).filter((a) => !a.terminal);
  return since(live.length ? at(live[live.length - 1].started) : null);
}

function lastHandback(task, at) {
  const done = (task.task.attempts || []).filter((a) => a.terminal);
  return done.length ? done[done.length - 1] : null;
}

// packetLabel names an intake packet by what its first event proposes.
function packetLabel(packet) {
  const event = packet.packet?.events?.[0];
  const spec = event?.data?.spec || {};
  const words = spec.intent || spec.question || spec.assertion || spec.question_answered;
  const more = packet.packet?.events?.length > 1 ? ` (+${packet.packet.events.length - 1} events)` : "";
  return (words ? `${event.type}: ${words}` : event?.type || "empty packet") + more;
}

export async function renderHome(todo) {
  const at = await times();
  const t = todo.totals;
  const hero = el("div", "hero",
    el("div", "", el("h1", "", "What's owed"), el("p", "lede", "A read-only view of what still needs attention.")),
    art("hero-art"));

  const inFlight = todo.in_flight.map((x) => row(x.ref.record_id, x.label, holder(x), liveSince(x, at)));
  const awaiting = todo.awaiting_acceptance.map((x) => {
    const back = lastHandback(x, at);
    const accepter = typeof x.accepter === "string" ? x.accepter : actorName(x.accepter);
    return row(x.ref.record_id, x.label, back ? actorName(back.actor) : "—",
      (back ? since(at(back.terminal.origin)) + " · " : "") + "accepter " + accepter);
  });
  const blocked = todo.blocked.map((x) => {
    const reasons = (x.task.reasons || []).map((r) => r.detail);
    return row(x.ref.record_id, x.label, (x.task.waiting_actors || []).map(actorName).join(", ") || "—",
      reasons.length === 1 ? "1 reason" : `${reasons.length} reasons`, "", reasons.length ? reasons : ["BLOCKED with no reason recorded"]);
  });
  const ready = todo.ready.map((x) => row(x.ref.record_id, x.label, actorName(x.task.expected_next_actor), "next actor"));
  const decisions = todo.open_decisions.map((x) => row(x.ref.record_id, x.label, actorName(x.decision?.waiting_actor), "awaiting a ruling", "blue"));
  const listed = todo.packets_not_accepted.filter((p) => p.disposition !== "rejected");
  const proposals = listed.map((p) => row("", "", actorName(p.packet?.author),
    p.disposition === "pending" ? "awaiting review" : p.disposition + (p.review?.reason ? `: ${p.review.reason}` : ""),
    p.disposition === "pending" ? "" : "amber"));
  listed.forEach((p, i) => proposals[i].firstChild.replaceChildren(mono(p.command_id, "strong"), el("span", "owed-label", packetLabel(p))));

  const omitted = todo.omitted?.ready?.omitted ? ` (${todo.omitted.ready.omitted} more omitted)` : "";
  const counts = [`${t.intake_unreviewed} unreviewed`];
  if (t.intake_correction_requested) counts.push(`${t.intake_correction_requested} correction requested`);
  if (t.intake_rejected) counts.push(`${t.intake_rejected} rejected, owing nothing`);
  const proposalMeaning = counts.join(" · ") + ".";

  return el("div", "screen",
    hero,
    el("div", "owed-grid",
      card("blue", "Work in progress", "Actively being worked on.", t.in_flight, inFlight),
      card("green", "Handed back / awaiting acceptance", "Handed back; waiting for acceptance.", t.awaiting_acceptance, awaiting),
      card("red", "Blocked work", "Can't proceed until resolved.", t.blocked, blocked),
      card("amber", "Ready to start", "Nothing blocks these." + omitted, t.ready, ready),
      card("purple", "Open decisions", "Needs a decision.", t.open_decisions, decisions),
      card("sky", "Proposals waiting for review", proposalMeaning, t.intake_unreviewed + t.intake_correction_requested, proposals)));
}
