// Home: one card per section of the todo view, in its order, each counted by
// todo's own totals. A row is one line: the record's title first, then who
// and when; a blocked row's reasons open under a one-line summary at its end. An empty card says
// so; nothing is hidden.
import { el, link, icon, disclose, facts, actorName, since, times, titles, fullTitle } from "./ui.js";

// card is one section: icon, title, count, then its rows, scrolling inside.
function card(glyph, tone, title, count, rows) {
  return el("section", "card",
    el("header", "card-head", el("span", "card-icon tone-text " + tone, icon(glyph)), el("h2", "", title), el("span", "count", String(count))),
    el("div", "card-body", rows.length ? el("ul", "rows", rows) : el("p", "empty", "empty...")));
}

// row opens its record; meta is the muted who-and-when line; more, when
// given, is a [summary, detail] pair that opens under the row.
function row(id, title, meta, more) {
  const words = meta.filter(Boolean);
  const main = el(id ? "a" : "div", "row-main", el("span", "row-title", title), el("span", "row-meta", words.map((m) => el("span", "", m))));
  main.title = [title, ...words].join("\n");
  if (id) main.href = "#/record/" + encodeURIComponent(id);
  const line = el("div", "row-line", main);
  const li = el("li", "row", line);
  if (more) {
    const button = el("button", "why", el("span", "", more[0]), icon("chevron"));
    button.type = "button";
    const detail = el("div", "why-detail", more[1]);
    disclose(button, detail, li);
    line.append(button);
    li.append(detail);
  }
  return li;
}

const holder = (task) => (task.task.attempt_holders || []).map((h) => actorName(h.actor)).join(", ");

function liveSince(task, at) {
  const live = (task.task.attempts || []).filter((a) => !a.terminal);
  return since(live.length ? at(live[live.length - 1].started) : null);
}

// A prerequisite's reason reads "prerequisite N (kind) is TRUTH: why"; its
// target is named by title and the why kept, in the ledger's own words.
const prerequisite = /^prerequisite \d+ \([^)]+\) is \w+: (?:(?:claim|decision|task) \S+ revision \d+ is )?(.*)$/;

// reasons is a blocked task's summary line and its full reasons, one each.
function reasons(task, names) {
  const list = task.task.reasons || [];
  if (!list.length) return ["No reason recorded", el("p", "", "BLOCKED with no reason recorded")];
  const prereqs = list.filter((r) => r.kind === "prerequisite" && prerequisite.test(r.detail));
  const summary = prereqs.length === list.length
    ? (list.length === 1 ? "1 prerequisite not met" : `${list.length} prerequisites not met`)
    : list.length === 1 ? list[0].detail.split(/[;.]/)[0] : `${list.length} reasons`;
  return [summary, el("ul", "why-list", list.map((r) => {
    const m = r.kind === "prerequisite" && r.detail.match(prerequisite);
    const target = r.target?.record_id;
    return el("li", "", m && target ? [link(target, names.get(target) || target), el("span", "muted", m[1])] : r.detail);
  }))];
}

// packetTitle names a proposal by what its first event proposes.
export function packetTitle(packet) {
  const events = packet.packet?.events || [];
  const spec = events[0]?.data?.spec || {};
  const words = spec.intent || spec.question || spec.assertion || spec.question_answered || events[0]?.type || "Empty proposal";
  return events.length > 1 ? `${words} (+${events.length - 1} more)` : words;
}

export async function renderHome(todo) {
  const [at, names] = await Promise.all([times(), titles()]);
  const t = todo.totals;
  const inFlight = todo.in_flight.map((x) => row(x.ref.record_id, fullTitle(x), [holder(x), liveSince(x, at)]));
  const awaiting = todo.awaiting_acceptance.map((x) => {
    const done = (x.task.attempts || []).filter((a) => a.terminal);
    const back = done[done.length - 1];
    return row(x.ref.record_id, fullTitle(x), [back && actorName(back.actor), back && since(at(back.terminal.origin)), "accepter " + actorName(x.accepter)]);
  });
  const blocked = todo.blocked.map((x) => row(x.ref.record_id, fullTitle(x),
    [(x.task.waiting_actors || []).map(actorName).join(", ")], reasons(x, names)));
  const ready = todo.ready.map((x) => row(x.ref.record_id, fullTitle(x), ["next " + actorName(x.task.expected_next_actor)]));
  const omitted = todo.omitted?.ready?.omitted;
  if (omitted) ready.push(el("li", "row more", `${omitted} more not listed`));
  const decisions = todo.open_decisions.map((x) => row(x.ref.record_id, fullTitle(x), ["waiting on " + actorName(x.decision?.waiting_actor)]));
  const proposals = todo.packets_not_accepted.filter((p) => p.disposition !== "rejected").map((p) =>
    row("", packetTitle(p), [actorName(p.packet?.author), p.disposition === "pending" ? "awaiting review" : null],
      p.disposition === "pending" ? null : [p.disposition === "correction-requested" ? "Correction requested" : p.disposition,
        facts([["Reason", p.review?.reason || "no reason recorded"], ["Reviewed by", actorName(p.review?.actor)]])]));

  return el("div", "screen home",
    card("progress", "blue", "Work in progress", t.in_flight, inFlight),
    card("back", "green", "Handed back", t.awaiting_acceptance, awaiting),
    card("blocked", "red", "Blocked", t.blocked, blocked),
    card("ready", "amber", "Ready to start", t.ready, ready),
    card("decision", "purple", "Open decisions", t.open_decisions, decisions),
    card("proposal", "sky", "Proposals", t.intake_unreviewed + t.intake_correction_requested, proposals));
}
