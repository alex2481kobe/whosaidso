// Home: the greeting, six stat cards counted by the todo view's own totals,
// then recent activity (or the list behind a picked stat card) beside the
// project summary. "In 7 days" is counted from the history's events
// (ui-derive.js). A list row is one line: the record's title first, then who
// and when; a blocked row's reasons open under a one-line summary at its end.
// Every note and message says only what the counts it reads say.
import { el, link, icon, disclose, facts, actorName, since, fullTitle, view, when, status } from "./ui.js";
import { activity, within, count, packetTitle } from "./ui-derive.js";
import { hero, statCard, head, more } from "./ui-parts.js";
import { mark } from "./ui-icons.js";

// focus is the stat card whose list replaces recent activity; "" shows activity.
let focus = "";

// greeting follows the local clock: morning 5-12, afternoon 12-17, evening 17-21, night 21-5.
export function greeting(hour = new Date().getHours()) {
  if (hour >= 5 && hour < 12) return ["Good morning!", "☀️"];
  if (hour >= 12 && hour < 17) return ["Good afternoon!", "🌤️"];
  if (hour >= 17 && hour < 21) return ["Good evening!", "🌆"];
  return ["Good night!", "🌙"];
}

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

// proposalNote splits the Proposals count into its two parts: packets no
// review has judged, and packets a review sent back for correction.
export function proposalNote(unreviewed, correction) {
  if (!unreviewed && !correction) return "No proposals waiting.";
  const parts = [];
  if (unreviewed) parts.push(`${unreviewed} awaiting review`);
  if (correction) parts.push(`${correction} correction requested`);
  return parts.join(", ") + ".";
}

// message is the summary footer's line. Each one claims only what its own
// test checked, most pressing first; "nothing is owed" needs every todo
// section and the intake to be empty.
export function message(t, attention) {
  const waiting = t.intake_unreviewed + t.intake_correction_requested + t.awaiting_acceptance + t.open_decisions;
  if (t.blocked) return ["Some work is blocked.", "Take a look."];
  if (waiting) return ["Some work is waiting on a review or a ruling.", ""];
  if (attention) return ["Some records need attention.", ""];
  if (t.in_flight || t.ready) return ["Work is in progress or ready to start.", "Keep building. ✨"];
  return ["Nothing is owed right now.", ""];
}

export async function renderHome(todo) {
  const [act, show] = await Promise.all([activity(), view("show")]);
  const t = todo.totals;
  const proposals = t.intake_unreviewed + t.intake_correction_requested;
  // [key, tone, count, label, note]; a note restates its own card's count and nothing else
  const cards = [
    ["progress", "blue", t.in_flight, "Work in progress", t.in_flight ? `${t.in_flight} being worked on.` : "No work in progress."],
    ["back", "green", t.awaiting_acceptance, "Handed back", t.awaiting_acceptance ? `${t.awaiting_acceptance} waiting for acceptance.` : "Nothing handed back."],
    ["blocked", "red", t.blocked, "Blocked", t.blocked ? `${t.blocked} blocked.` : "Nothing blocked."],
    ["ready", "amber", t.ready, "Ready to start", t.ready ? `${t.ready} ready to pick up.` : "Nothing ready to start."],
    ["decision", "purple", t.open_decisions, "Open decisions", t.open_decisions ? `${t.open_decisions} waiting for a ruling.` : "No open decisions."],
    ["proposal", "sky", proposals, "Proposals", proposalNote(t.intake_unreviewed, t.intake_correction_requested)],
  ];
  const lists_ = lists(todo, act.at, act.names);
  const main = el("div", "home-main");
  const stats = el("div", "stats six");
  // pick shows a card's list in place of recent activity; picking it again,
  // or "Recent activity", goes back.
  const pick = (key) => { focus = focus === key ? "" : key; paint(); };
  function paint() {
    stats.replaceChildren(...cards.map(([key, tone, n, label, note]) => statCard({
      glyph: key, tone, value: n, label, note, selected: focus === key, onPick: () => pick(key),
    })));
    const card = cards.find((c) => c[0] === focus);
    main.replaceChildren(card
      ? el("section", "panel fill", head(card[0], card[1], card[3], `${card[2]} ${card[2] === 1 ? "item" : "items"}`, more("Recent activity", () => pick(focus))),
        el("div", "panel-scroll", lists_[focus].length ? el("ul", "rows", lists_[focus]) : el("p", "empty", "empty...")))
      : el("section", "panel fill", head("progress", "blue", "Recent activity", null, more("View all", () => { location.hash = "#/history"; })),
        el("div", "panel-scroll", act.rows.length ? el("div", "act-list", act.rows.slice(-30).reverse().map(activityRow)) : el("p", "empty", "empty..."))));
  }
  paint();

  const records = show.records;
  const kind = (k) => records.filter((r) => r.fact.kind === k);
  const everything = records.map((r) => r.ref.record_id).concat((show.sources || []).map((s) => s.intake.source_id));
  const tasks = kind("TASK"), claims = kind("CLAIM"), instruments = kind("INSTRUMENT");
  const closed = count(tasks, (r) => r.task?.status === "CLOSED");
  const withdrawn = count(instruments, (r) => status(r)[0] === "Withdrawn");
  const added = count(everything, (id) => within(act.created.get(id), 7));
  const proven = count(claims, (r) => r.claim?.status === "PROVEN");
  const metric = (glyph, value, label, note, tone) => el("div", "metric", el("span", "tile round tone-tint slate", icon(glyph)),
    el("div", "metric-words", el("span", "metric-value", String(value)), el("span", "metric-label", label), el("span", "metric-note tone-text " + tone, note)));
  const [strong, rest] = message(t, (todo.attention || []).length);
  const owl = el("img", "summary-owl");
  owl.src = "/owl.png";
  owl.alt = "";
  const summary = el("section", "panel", head("bars", "blue", "Project summary", null, more("View records", () => { location.hash = "#/records"; })),
    el("div", "metrics",
      metric("file", everything.length, "Total records", `+${added} in 7 days`, added ? "green" : "muted"),
      metric("check", closed, "Closed tasks", tasks.length ? `${Math.round((closed / tasks.length) * 100)}% of tasks` : "No tasks yet", "muted"),
      metric("claim", claims.length, "Claims", claims.length ? `${proven} proven` : "None yet", proven ? "green" : "muted"),
      metric("box", instruments.length, "Instruments", !instruments.length ? "None yet" : withdrawn ? `${withdrawn} withdrawn` : "None withdrawn", !instruments.length ? "muted" : withdrawn ? "red" : "green")),
    el("div", "summary-foot", owl, el("p", "", el("strong", "", strong), rest ? " " + rest : ""), el("span", "summary-tag", "Small steps. Solid records.")));

  const [hello, emoji] = greeting();
  return el("div", "screen home",
    hero("home", hello, emoji, ["Here's what's happening in ", todo.project, "."], "Track progress, review recent activity, and keep things moving."),
    stats,
    el("div", "home-body", main, el("div", "home-side", summary)));
}
