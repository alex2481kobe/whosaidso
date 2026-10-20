// Home: six work-status filters counted by the todo view's own totals,
// then recent activity (or the list behind a picked filter) beside the
// project summary. "In 7 days" is counted from the history's events
// (ui-derive.js). A list row is one line: the record's title first, then who
// and when; a blocked row's reasons open under a one-line summary at its end.
import { el, link, icon, disclose, facts, actorName, since, fullTitle, view, when, status } from "./ui.js";
import { activity, within, count, packetTitle } from "./ui-derive.js";
import { hero, chip, more } from "./ui-parts.js";
import { mark } from "./ui-icons.js";

// focus is the filter whose list replaces recent activity; "" shows activity.
let focus = null;

// row opens its record; meta is the muted who-and-when line; extra, when
// given, is a [summary, detail] pair that opens under the row.
function row(id, title, meta, extra) {
  const words = meta.filter(Boolean);
  const main = el(id ? "a" : "div", "row-main", el("span", "row-title", title), el("span", "row-meta", words.map((m) => el("span", "", m))));
  main.title = [title, ...words].join("\n");
  if (id) main.href = "#/record/" + encodeURIComponent(id);
  const line = el("div", "row-line", main);
  const li = el("li", "row", line);
  if (extra) {
    const button = el("button", "why", el("span", "", extra[0]), icon("chevron"));
    button.type = "button";
    const detail = el("div", "why-detail", extra[1]);
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

// lists are the rows behind each stat card, as the todo view gives them.
function lists(todo, at, names) {
  const awaiting = (x) => {
    const done = (x.task.attempts || []).filter((a) => a.terminal);
    const back = done[done.length - 1];
    return row(x.ref.record_id, fullTitle(x), [back && actorName(back.actor), back && since(at(back.terminal.origin)), "accepter " + actorName(x.accepter)]);
  };
  const ready = todo.ready.map((x) => row(x.ref.record_id, fullTitle(x), ["next " + actorName(x.task.expected_next_actor)]));
  if (todo.omitted?.ready?.omitted) ready.push(el("li", "row more-rows", `${todo.omitted.ready.omitted} more not listed`));
  return {
    progress: todo.in_flight.map((x) => row(x.ref.record_id, fullTitle(x), [holder(x), liveSince(x, at)])),
    back: todo.awaiting_acceptance.map(awaiting),
    blocked: todo.blocked.map((x) => row(x.ref.record_id, fullTitle(x), [(x.task.waiting_actors || []).map(actorName).join(", ")], reasons(x, names))),
    ready,
    decision: todo.open_decisions.map((x) => row(x.ref.record_id, fullTitle(x), ["waiting on " + actorName(x.decision?.waiting_actor)])),
    proposal: todo.packets_not_accepted.filter((p) => p.disposition !== "rejected").map((p) =>
      row("", packetTitle(p), [actorName(p.packet?.author), p.disposition === "pending" ? "awaiting review" : null],
        p.disposition === "pending" ? null : [p.disposition === "correction-requested" ? "Correction requested" : p.disposition,
          facts([["Reason", p.review?.reason || "no reason recorded"], ["Reviewed by", actorName(p.review?.actor)]])])),
  };
}

// activityRow is one event on one line: when, what, which record, who.
export function activityRow(r) {
  const a = el("a", "act-row", el("span", "act-time", when(r.time)), el("span", "act-what", mark(r.group), el("span", "", r.what)),
    el("span", "act-title", r.title), el("span", "act-by", r.proposer));
  a.href = r.target ? "#/record/" + encodeURIComponent(r.target) : "#/history";
  a.title = [r.what, r.title, r.proposer].filter(Boolean).join("\n");
  return a;
}

export async function renderHome(todo) {
  const [act, show] = await Promise.all([activity(), view("show")]);
  const t = todo.totals;
  const categories = [
    ["blocked", t.blocked, "Blocked"],
    ["back", t.awaiting_acceptance, "Awaiting acceptance"],
    ["progress", t.in_flight, "In progress"],
    ["ready", t.ready, "Ready"],
    ["decision", t.open_decisions, "Decisions"],
    ["proposal", t.intake_unreviewed + t.intake_correction_requested, "Proposals"],
  ];
  const lists_ = lists(todo, act.at, act.names);
  const tabs = el("nav", "overview-filters");
  tabs.setAttribute("aria-label", "Work status");
  const main = el("section", "overview-list");
  const pick = (key) => { focus = key; paint(); };
  function paint() {
    const button = (key, label) => chip(label, focus === key, () => pick(key));
    tabs.replaceChildren(...categories.map(([key, n, label]) => button(key, `${label} ${n}`)), button("", "Recent activity"));
    const selected = categories.find(([key]) => key === focus);
    main.replaceChildren(selected
      ? el("div", "", lists_[focus].length ? el("ul", "rows", lists_[focus]) : el("p", "empty", "Nothing here."))
      : el("div", "", el("div", "overview-list-head", more("View all activity", () => { location.hash = "#/history"; })),
        act.rows.length ? el("div", "act-list", act.rows.slice(-15).reverse().map(activityRow)) : el("p", "empty", "No activity yet.")));
  }
  if (focus === null) focus = categories.find(([, n]) => n > 0)?.[0] || "";
  paint();
  const records = show.records;
  const tasks = records.filter((r) => r.fact.kind === "TASK");
  const claims = records.filter((r) => r.fact.kind === "CLAIM");
  const instruments = records.filter((r) => r.fact.kind === "INSTRUMENT");
  const everything = records.map((r) => r.ref.record_id).concat((show.sources || []).map((s) => s.intake.source_id));
  const summary = el("details", "overview-summary", el("summary", "", "Project totals"), facts([
    ["Records", everything.length], ["Added in 7 days", count(everything, (id) => within(act.created.get(id), 7))],
    ["Closed tasks", count(tasks, (r) => r.task?.status === "CLOSED")],
    ["Claims", claims.length], ["Proven claims", count(claims, (r) => r.claim?.status === "PROVEN")],
    ["Instruments", instruments.length], ["Withdrawn instruments", count(instruments, (r) => status(r)[0] === "Withdrawn")],
  ]));
  return el("div", "screen home", hero("Overview"), tabs, main, summary);
}
