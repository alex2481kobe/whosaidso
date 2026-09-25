// Home: the greeting, six stat cards counted by the todo view's own totals,
// then recent activity (or the list behind a picked stat card), next steps
// derived from the same totals, and the project summary. Sparklines and
// "this week" are counted from the history's events (ui-derive.js). A list
// row is one line: the record's title first, then who and when; a blocked
// row's reasons open under a one-line summary at its end.
import { el, link, icon, disclose, facts, actorName, since, fullTitle, view, when, status } from "./ui.js";
import { activity, perDay, within, count, packetTitle } from "./ui-derive.js";
import { hero, statCard, sparkline, head, more } from "./ui-parts.js";
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

// steps are up to three next steps the totals call for, most urgent first;
// each button opens the matching list.
function steps(t, proposals, pick) {
  const all = [
    [proposals, "Triage new proposals", "Review and decide on pending proposals.", "View proposals", "proposal", "blue"],
    [t.blocked, "Look at blocked items", "Make sure nothing is stuck.", "View blocked", "blocked", "red"],
    [t.awaiting_acceptance, "Review handed-back work", "Accept or hold what came back.", "View handed back", "back", "green"],
    [t.open_decisions, "Settle open decisions", "Rule on what is waiting for an answer.", "View decisions", "decision", "purple"],
    [t.ready, "Plan upcoming work", "Browse ready-to-start items.", "View ready to start", "ready", "amber"],
  ].filter((s) => s[0] > 0).slice(0, 3);
  if (!all.length) return [el("p", "step-none", "Nothing needs you right now. Enjoy the quiet.")];
  return all.map(([, title, sub, label, key, tone]) => {
    const b = el("button", "step-button tone-soft " + tone, label);
    b.type = "button";
    b.addEventListener("click", () => pick(key, true));
    return el("div", "step", el("span", "tile small tone-tint amber", icon("ready")), el("div", "step-words", el("strong", "", title), el("span", "", sub)), b);
  });
}

// message is the summary footer's line, chosen by the project's state.
function message(t, proposals, total) {
  if (!total) return ["Nothing here yet.", "Capture a first record."];
  if (t.blocked) return ["Some work is stuck.", "Take a look."];
  if (t.in_flight || t.awaiting_acceptance) return ["Nice work!", "Keep building. ✨"];
  if (proposals || t.open_decisions) return ["Your call.", "Things are waiting on you."];
  return ["All clear!", "Everything is in order."];
}

export async function renderHome(todo) {
  const [act, show] = await Promise.all([activity(), view("show")]);
  const t = todo.totals;
  const proposals = t.intake_unreviewed + t.intake_correction_requested;
  // packets no review has judged yet; judged ones are in act.captured
  const pending = todo.packets_not_accepted.filter((p) => p.disposition === "pending").map((p) => (p.packet?.captured_at ? new Date(p.packet.captured_at) : null));
  const series = (test) => perDay(act.rows.filter((r) => test(r.type)).map((r) => r.time));
  // [key, tone, count, label, note when zero, note, sparkline counts, what the sparkline counts]
  const cards = [
    ["progress", "blue", t.in_flight, "Work in progress", "No active work right now.", (n) => `${n} being worked on.`, series((x) => x === "task.start"), "Work started"],
    ["back", "green", t.awaiting_acceptance, "Handed back", "Nothing handed back.", (n) => `${n} waiting for acceptance.`, series((x) => x === "attempt.terminal"), "Handbacks"],
    ["blocked", "red", t.blocked, "Blocked", "All clear!", (n) => `${n} need${n === 1 ? "s" : ""} unblocking.`, series((x) => x === "blocker.hold"), "Holds"],
    ["ready", "amber", t.ready, "Ready to start", "Nothing waiting.", (n) => `${n} ready to pick up.`, series((x) => x === "task.create"), "Tasks created"],
    ["decision", "purple", t.open_decisions, "Open decisions", "No open decisions.", (n) => `${n} waiting for a ruling.`, series((x) => x.startsWith("decision.")), "Decision events"],
    ["proposal", "sky", proposals, "Proposals", "No proposals waiting.", (n) => `${n} waiting for review.`, perDay(act.captured.concat(pending)), "Proposals captured"],
  ];
  const lists_ = lists(todo, act.at, act.names);
  const main = el("div", "home-main");
  const stats = el("div", "stats six");
  // pick shows a card's list in place of recent activity; picking it again,
  // or "Recent activity", goes back. A next-step button always shows its list.
  const pick = (key, keep) => { focus = focus === key && !keep ? "" : key; paint(); };
  function paint() {
    stats.replaceChildren(...cards.map(([key, tone, n, label, zero, note, values, what]) => statCard({
      glyph: key, tone, value: n, label, note: n ? note(n) : zero, selected: focus === key, onPick: () => pick(key),
      spark: sparkline(values, tone, false, `${what} per day, last 14 days`),
    })));
    const card = cards.find((c) => c[0] === focus);
    main.replaceChildren(card
      ? el("section", "panel fill", head(card[0], card[1], card[3], `${card[2]} ${card[2] === 1 ? "item" : "items"}`, more("Recent activity", () => pick(focus))),
        el("div", "panel-scroll", lists_[focus].length ? el("ul", "rows", lists_[focus]) : el("p", "empty", "empty...")))
      : el("section", "panel fill", head("progress", "blue", "Recent activity", null, more("View all", () => { location.hash = "#/history"; })),
        el("div", "panel-scroll", act.rows.length ? el("div", "act-list", act.rows.slice(-60).reverse().map(activityRow)) : el("p", "empty", "empty..."))));
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
  const [strong, rest] = message(t, proposals, everything.length);
  const owl = el("img", "summary-owl");
  owl.src = "/owl.png";
  owl.alt = "";
  const summary = el("section", "panel", head("bars", "blue", "Project summary", null, more("View records", () => { location.hash = "#/records"; })),
    el("div", "metrics",
      metric("file", everything.length, "Total records", `+${added} this week`, added ? "green" : "muted"),
      metric("check", closed, "Closed tasks", tasks.length ? `${Math.round((closed / tasks.length) * 100)}% of tasks` : "No tasks yet", "muted"),
      metric("claim", claims.length, "Claims", claims.length ? `${proven} proven` : "None yet", proven ? "green" : "muted"),
      metric("box", instruments.length, "Instruments", !instruments.length ? "None yet" : withdrawn ? `${withdrawn} withdrawn` : "All active", !instruments.length ? "muted" : withdrawn ? "red" : "green")),
    el("div", "summary-foot", owl, el("p", "", el("strong", "", strong), rest ? " " + rest : ""), el("span", "summary-tag", "Small steps. Solid records.")));

  const [hello, emoji] = greeting();
  return el("div", "screen home",
    hero("home", hello, emoji, ["Here's what's happening in ", todo.project, "."], "Track progress, review recent activity, and keep things moving."),
    stats,
    el("div", "home-body", main,
      el("div", "home-side",
        el("section", "panel", head("target", "red", "Next steps", "Suggested next steps to keep things moving."), el("div", "steps", steps(t, proposals, pick))),
        summary)));
}
