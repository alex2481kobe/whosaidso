// One record's detail: the shared frame (back link, the full title, a
// labelled details row led by the status and ending in Copy id, then titled
// sections of one-line items that open on click, each as tall as its items),
// the task and plan screens with why a blocked task waits and on whom, the
// "Changes over time" and "Needs attention" sections every kind shares, and
// the dispatch to ui-kinds.js for claims, decisions, instruments and
// sources. A task reads from continue; dates are when WhoSaidSo captured the
// events (times()).
import { el, facts, item, section, copyButton, openButton, view, times, titles, actorName, status, stamp, when, titleCase, fullTitle, icon, project } from "./ui.js";
import { hero } from "./ui-parts.js";
import { kinds } from "./ui-icons.js";
import { renderClaim, renderDecision, renderInstrument, renderSource } from "./ui-kinds.js";

// origin is the screen a record was opened from, so Back returns there: Home,
// Records (with its query) or History. Moving from record to record keeps
// it; a record opened directly goes back to Records.
const origins = [[/^#\/records/, "records"], [/^#\/history/, "history"], [/^(#\/?)?$/, "home"]];
let origin = ["#/records", "records"];
window.addEventListener("hashchange", (e) => {
  const from = new URL(e.oldURL).hash;
  if (!location.hash.startsWith("#/record/") || from.startsWith("#/record/")) return;
  const found = origins.find(([pattern]) => pattern.test(from));
  origin = found ? [from || "#/", found[1]] : ["#/records", "records"];
});

// frame is every detail screen: the hero naming the record's kind, then one
// panel with the back link, the full title and the details row (the status
// and any chip beside it first, Copy id last), and the sections below.
export function frame({ id, kind, title, tone, statusLabel, chip, details }, ...sections) {
  const back = el("a", "back", "← Back to " + origin[1]);
  back.href = origin[0];
  const shown = sections.filter(Boolean);
  const grid = el("div", "sections", shown);
  grid.style.setProperty("--cols", String(shown.length === 4 ? 2 : Math.min(3, Math.max(1, shown.length))));
  const row = facts([["Status", [el("span", "tone-text " + tone, statusLabel), chip?.[0]]], ...details], "facts details");
  row.append(el("div", "details-end", copyButton("Copy id", id)));
  const k = kinds[kind] || kinds.task;
  return el("div", "screen detail",
    hero("detail", k.one, el("span", "tile tone-tint " + k.tone, icon(k.icon)), [`One ${k.one.toLowerCase()} in `, project(), "."], "What the ledger records about it."),
    el("section", "panel detail-head", back, el("h2", "detail-title", title), row, chip?.[1]),
    grid);
}

// panel is a titled section holding labelled facts rather than a list.
export const panel = (title, body) => el("section", "panel", el("h2", "", el("span", "", title)), el("div", "panel-body", body));

// span dates a record by its history: the first and the latest event naming it.
export function span(history, at) {
  const events = history.events;
  return [events.length ? at(events[0].origin) : null, events.length ? at(events[events.length - 1].origin) : null];
}

// attention is what the continue view raises about the record, less the kinds
// the screen already shows in full (a blocked task's owed reasons).
export function attention(answer, shown = []) {
  const notes = (answer.attention || []).filter((a) => !shown.includes(a.kind));
  if (!notes.length) return null;
  return section("Needs attention", notes.map((a) => item(a.label || a.ref?.record_id || a.kind, titleCase(a.kind.replace(/-/g, " ")),
    facts([["Reason", a.reason], ["Waiting on", a.waiting_actor && actorName(a.waiting_actor)],
      ["Record", a.ref?.record_id && a.ref.record_id !== answer.record?.record_id ? openButton(a.ref.record_id) : ""]]), "amber")));
}

const list = (xs) => (xs.length ? el("ul", "plain", xs.map((x) => el("li", "", x))) : "");
export const short = (commit) => (commit ? commit.slice(0, 7) : "UNKNOWN");

// fieldName reads a change path for people: a keyed list entry by its list.
const fieldName = (path) => path.replace(/^acceptance_criteria\[[^\]]+\]\.?/, "acceptance criterion ").replace(/^prerequisites\[[^\]]+\]\.?/, "plan item ")
  .replace(/_/g, " ").replace("criterion criterion", "criterion").trim();

// changes is every amendment of the record and, for a claim, every code
// change a proof set a failing run aside for, oldest first.
export function changes(answer, at, names, proofs = []) {
  const rows = answer.amendments.map((a) => {
    const groups = { added: [], removed: [], changed: [] };
    for (const ch of a.changes) {
      const target = ch.path.startsWith("prerequisites[") && (ch.after?.target || ch.before?.target);
      groups[ch.change]?.push(target ? names.get(target.record_id) || target.record_id
        : fieldName(ch.path) + (ch.change === "changed" ? `: ${JSON.stringify(ch.before)} to ${JSON.stringify(ch.after)}` : ""));
    }
    return [a.origin, item(`Revision ${a.revision.revision}`, when(at(a.origin)), facts([["Approved by", actorName(a.review?.actor)],
      ["Reason", a.review?.reason], ["Added", list(groups.added)], ["Removed", list(groups.removed)], ["Changed", list(groups.changed)]]))];
  });
  for (const p of proofs) for (const ev of p.admission.evidence) {
    const cc = ev.code_change;
    if (!cc) continue;
    rows.push([p.origin, item(`Code updated from ${short(cc.from.commit)} to ${short(cc.to.commit)}`, when(at(p.origin)),
      facts([["Approved by", actorName(p.admission.judgment.actor)], ["Reason", p.admission.judgment.reason], ["Changed paths", list(cc.changed_paths)]]))]);
  }
  rows.sort((a, b) => a[0].sequence - b[0].sequence || a[0].event_index - b[0].event_index);
  return section("Changes over time", rows.map((r) => r[1]));
}

export async function renderDetail(id) {
  const [at, names, history, answer] = await Promise.all([times(), titles(), view("history", { id }), view("continue", { id })]);
  if (answer.result === "UNKNOWN") {
    const shown = await view("show", { id });
    if (shown.sources?.length) return renderSource(shown.sources[0], history, at, names);
    return el("div", "screen", el("p", "notice", `UNKNOWN: ${answer.reason}`));
  }
  const root = answer.records[`${answer.record.record_id}@${answer.record.revision}`];
  const context = { id, root, answer, history, at, names, dates: span(history, at) };
  if (root.claim) return renderClaim(context, await view("show", { id, stale: "1" }));
  if (root.decision) return renderDecision(context);
  if (root.instrument) return renderInstrument(context);
  return renderTask(context);
}

// criteria: Met only when an admitted close witnesses the criterion; the
// ledger never records "not met", so anything else is UNKNOWN with why.
function criteria(root) {
  const closure = root.task.closure;
  const witnessed = new Set((closure?.acceptance_witnesses || []).map((w) => `${w.criterion_id}@${w.criterion_revision}`));
  const why = closure ? `closed ${closure.outcome} without an acceptance witness for it` : "no acceptance witness until an admitted close";
  return section("Acceptance criteria", root.fact.task.acceptance_criteria.map((c) => {
    const met = witnessed.has(`${c.id}@${c.revision}`);
    return item(c.criterion, met ? "Met" : "UNKNOWN", facts([["Criterion", c.criterion], ["Status", met ? "Met: an admitted close witnesses it" : "UNKNOWN: " + why]]), met ? "green" : "muted");
  }));
}

function attempts(root, at) {
  const rows = root.task.attempts.map((a, i) => item(`Attempt ${i + 1} by ${actorName(a.actor)}`, a.terminal ? titleCase(a.terminal.outcome) : "In progress",
    facts([["Started", stamp(at(a.started))], ["Handed back", a.terminal && stamp(at(a.terminal.origin))], ["Outcome", a.terminal?.outcome],
      ["Reason", a.terminal?.reason], ["Next action", a.terminal?.next_action]]), a.terminal ? "" : "blue"));
  const closure = root.task.closure;
  if (closure) rows.push(item(`Closed by ${actorName(closure.closer?.actor)}`, titleCase(closure.outcome), facts([["Closed", stamp(at(closure.origin))], ["Outcome", closure.outcome]])));
  return section("Attempts and handbacks", rows);
}

// planItems: each prerequisite with its own truth and, when not met, why.
function planItems(answer) {
  const words = { TRUE: ["Met", "green"], FALSE: ["Not met", "red"] };
  return section("Plan items", answer.owed.items.map((p) => {
    const target = p.target.record_id;
    const [word, tone] = words[p.satisfied] || ["UNKNOWN", "muted"];
    const reason = (answer.owed.reasons || []).find((r) => r.target?.record_id === target)?.detail || (p.satisfied === "TRUE" ? "" : "no reason recorded");
    return item(answer.records[`${target}@${p.target.revision}`]?.label || target, word,
      facts([["Kind", titleCase(p.kind)], ["Status", p.status], ["Waived", p.waived ? "Yes" : ""], ["Reason", reason], ["Record", openButton(target)]]), tone);
  }));
}

// blocked is what a blocked task waits on: each owed reason, whom it waits
// on, and the hold or record it names. An unknown actor stays UNKNOWN.
function blocked(answer, names) {
  const reasons = answer.owed.reasons || [];
  if (!reasons.length) return null;
  return section("What it waits on", reasons.map((r) => {
    const target = r.target?.record_id;
    return item(r.detail || titleCase(r.kind.replace(/-/g, " ")), actorName(r.waiting_actor),
      facts([["Kind", titleCase(r.kind.replace(/-/g, " "))], ["Reason", r.detail || "no reason recorded"], ["Waiting on", actorName(r.waiting_actor)],
        ["Hold", r.blocker_id ? el("span", "mono", r.blocker_id) : ""],
        ["Record", target ? el("span", "id-line", el("span", "", names.get(target) || target), openButton(target)) : ""]]), "red");
  }));
}

function renderTask({ id, root, answer, at, names, dates }) {
  const [label, tone] = status(root);
  const items = answer.owed.items || [];
  const plan = (root.fact.task.prerequisites || []).length > 0;
  return frame({ id, kind: "task", title: fullTitle(root), tone, statusLabel: label, details: [["Subject", root.fact.task.subject],
    ["Next actor", answer.owed.next_actor ? actorName(answer.owed.next_actor) : ""],
    ["Author", actorName(root.author?.actor)], ["Approved by", actorName(root.admitted_by)],
    ["Progress", plan ? `${items.filter((i) => i.satisfied === "TRUE").length} of ${items.length} met` : ""],
    ["Created", stamp(dates[0])], ["Updated", stamp(dates[1])]] },
  blocked(answer, names), plan ? planItems(answer) : null, criteria(root), attempts(root, at),
  attention(answer, ["task-blocked-owed"]), changes(answer, at, names));
}
