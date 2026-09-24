// One record's detail: the shared frame (back link, title, copy buttons,
// status line, meta row), the task and the plan screens, and the dispatch to
// ui-kinds.js for claims, decisions, instruments and sources. A task reads
// from continue; dates are when WhoSaidSo captured the events (times()).
import { el, mono, link, icon, art, copyButton, view, times, actorName, status, stamp, day, when, truth, titleCase } from "./ui.js";
import { renderClaim, renderDecision, renderInstrument, renderSource } from "./ui-kinds.js";

// frame is every detail screen's head: back link, "id — label", copy
// buttons, the status line with its sentence, and the meta row.
export function frame({ id, label, tone, statusWord, statusLabel, sentence, command, meta }, ...body) {
  const back = el("a", "back", icon("back"), "Back to records");
  back.href = "#/records";
  return el("div", "screen detail",
    back,
    el("h1", "detail-title", mono(id, "title-id"), el("span", "dash", " — "), el("span", "", label)),
    el("div", "detail-actions", copyButton("Copy id", id), copyButton("Copy command", command, "command")),
    el("div", "status-line tone-" + tone, el("span", "dot big"), el("strong", "", statusWord + ": "), el("strong", "tone-text " + tone, statusLabel)),
    el("p", "status-sentence", sentence),
    el("dl", "meta", meta.map(([k, v]) => el("div", "", el("dt", "", k), el("dd", "", v)))),
    ...body);
}

export const panel = (title, ...children) => el("section", "panel", el("h2", "", title), ...children);

// span dates a record by its history: the first and the latest event naming it.
export function span(history, at) {
  const events = history.events;
  return [events.length ? at(events[0].origin) : null, events.length ? at(events[events.length - 1].origin) : null];
}

export async function renderDetail(id) {
  const [at, history, answer] = await Promise.all([times(), view("history", { id }), view("continue", { id })]);
  if (answer.result === "UNKNOWN") {
    const shown = await view("show", { id });
    if (shown.sources?.length) return renderSource(shown.sources[0], history, at);
    return el("div", "screen", el("section", "notice", `UNKNOWN: ${answer.reason}`));
  }
  const root = answer.records[`${answer.record.record_id}@${answer.record.revision}`];
  const context = { id, root, answer, history, at, dates: span(history, at) };
  if (root.claim) return renderClaim(context, await view("show", { id, stale: "1" }));
  if (root.decision) return renderDecision(context);
  if (root.instrument) return renderInstrument(context);
  return (root.fact.task.prerequisites || []).length ? renderPlan(context) : renderTask(context);
}

function taskFrame({ id, root, answer, dates }, meta, ...body) {
  const [label, tone] = status(root);
  return frame({ id, label: root.label, tone, statusWord: "Status", statusLabel: label, sentence: root.fact.task.subject,
    command: `whosaidso continue ${id}`, meta }, ...body);
}

// criteria: Met only when an admitted close witnesses the criterion; the
// ledger never records "not met", so anything else is UNKNOWN with why.
function criteria(root) {
  const closure = root.task.closure;
  const witnessed = new Set((closure?.acceptance_witnesses || []).map((w) => `${w.criterion_id}@${w.criterion_revision}`));
  const why = closure ? `closed ${closure.outcome} without an acceptance witness for it` : "no acceptance witness until an admitted close";
  return panel("Acceptance criteria", el("ul", "checks", root.fact.task.acceptance_criteria.map((c) => {
    const met = witnessed.has(`${c.id}@${c.revision}`);
    return el("li", "", el("span", "bullet"), el("span", "check-text", c.criterion),
      el("span", "verdict " + (met ? "green" : "muted"), el("span", "mark " + (met ? "yes" : "unknown")), met ? "Met" : "UNKNOWN",
        met ? null : el("small", "", why)));
  })));
}

function attempts(root, at) {
  const rows = [];
  root.task.attempts.forEach((a, i) => {
    rows.push([at(a.started), `Attempt ${i + 1} by ${actorName(a.actor)}`, a.terminal ? "" : "in flight"]);
    if (a.terminal) rows.push([at(a.terminal.origin), `Handback by ${actorName(a.actor)}: ${a.terminal.outcome}`, `“${a.terminal.reason}”`]);
  });
  const closure = root.task.closure;
  if (closure) rows.push([at(closure.origin), `Closed by ${actorName(closure.closer?.actor)}: ${closure.outcome}`, ""]);
  return panel("Attempts and handbacks", rows.length ? el("ol", "timeline", rows.map(([t, title, quote]) =>
    el("li", "", el("span", "tl-dot"), el("span", "tl-time", when(t)), el("div", "", el("strong", "", title), quote ? el("p", "quote", quote) : null))))
    : el("p", "none", "none"));
}

function owedNext(answer, root) {
  const owed = answer.owed;
  const done = root.task.attempts.filter((a) => a.terminal);
  const last = done[done.length - 1];
  const [label] = status(root);
  const lines = [el("p", "", `${label}. Next owed by ${actorName(owed.next_actor)}.`)];
  for (const r of owed.reasons || []) lines.push(el("p", "", r.detail));
  if (last && root.task.status !== "CLOSED") lines.push(el("p", "", `The last handback asks: ${last.terminal.next_action}`));
  const caption = el("div", "caption", art("sprig"), el("span", "scribble", root.task.status === "CLOSED" ? "All done." : "Step by step."));
  return el("section", "owed-next", el("span", "owed-icon", icon("owed")), el("div", "", el("h2", "", "What's owed next"), ...lines), caption);
}

function renderTask(c) {
  const { root, answer, dates, at } = c;
  return taskFrame(c, [["Author", actorName(root.author?.actor)], ["Approved by", actorName(root.admitted_by)],
    ["Created", mono(stamp(dates[0]))], ["Updated", mono(stamp(dates[1]))], ["Next owed", actorName(answer.owed.next_actor)]],
  el("div", "two-col", criteria(root), attempts(root, at)),
  owedNext(answer, root));
}

// ---- plans: a task whose prerequisites are its items
function itemReason(answer, item) {
  const reason = (answer.owed.reasons || []).find((r) => r.target?.record_id === item.target.record_id);
  if (reason) return reason.detail;
  return item.satisfied === "TRUE" ? "" : "no reason recorded";
}

// fieldName reads a change path for people: a keyed list entry by its list.
const fieldName = (path) => path.replace(/^acceptance_criteria\[[^\]]+\]\.?/, "acceptance criterion ").replace(/^prerequisites\[[^\]]+\]\.?/, "plan item ").replace(/_/g, " ").trim();

function changeGroups(answer) {
  const groups = { added: [], removed: [], changed: [] };
  const name = (ref) => answer.records[`${ref?.record_id}@${ref?.revision}`]?.label || ref?.record_id;
  for (const a of answer.amendments) for (const ch of a.changes) {
    const item = ch.path.startsWith("prerequisites[") && (ch.after?.target || ch.before?.target);
    groups[ch.change]?.push(item ? name(item) : fieldName(ch.path) + (ch.change === "changed" ? `: ${JSON.stringify(ch.before)} → ${JSON.stringify(ch.after)}` : ""));
  }
  return groups;
}

function revisionCard(answer, at, root) {
  const last = answer.amendments[answer.amendments.length - 1];
  if (!last) return null;
  const list = el("ul", "change-list", last.changes.map((ch) => el("li", "", mono(ch.path), ` ${ch.change}`)));
  list.hidden = true;
  const toggle = el("button", "button", "View changes");
  toggle.type = "button";
  toggle.addEventListener("click", () => { list.hidden = !list.hidden; toggle.textContent = list.hidden ? "View changes" : "Hide changes"; });
  return el("section", "revision-card", el("span", "owed-icon", icon("owed")),
    el("div", "", el("strong", "", `Revision ${last.revision.revision} changed ${last.changes.length} fields`),
      el("p", "muted", `${when(at(last.origin))} · reviewed by ${actorName(last.review?.actor)}`),
      last.review?.reason ? el("p", "quote", `“${last.review.reason}”`) : null, list), toggle);
}

function renderPlan(c) {
  const { root, answer, dates, at } = c;
  const items = answer.owed.items;
  const met = items.filter((i) => i.satisfied === "TRUE").length;
  const groups = changeGroups(answer);
  const group = (title, tone, list) => [el("h3", "tone-text " + tone, title), el("ul", "dots", (list.length ? list : ["None"]).map((x) => el("li", "", x)))];
  return taskFrame(c, [["Author", actorName(root.author?.actor)], ["Created", day(dates[0])], ["Updated", day(dates[1])],
    ["Items", String(items.length)], ["Progress", `${met} / ${items.length}`], ["Revision", String(root.current_revision)]],
  el("div", "plan-cols",
    el("div", "stack",
      panel("Plan items", el("ul", "checks", items.map((item) => {
        const t = truth(item.satisfied);
        const target = answer.records[`${item.target.record_id}@${item.target.revision}`];
        const reason = itemReason(answer, item);
        return el("li", "", el("span", "bullet tone-" + ({ TRUE: "green", FALSE: "red" }[t] || "muted")),
          el("span", "check-text", link(item.target.record_id, target?.label || item.target.record_id, "plain-link"),
            el("small", "", `${titleCase(item.kind)} · ${item.status}${item.waived ? " · waived" : ""}`)),
          el("span", "verdict " + ({ TRUE: "green", FALSE: "red" }[t] || "muted"), el("span", "mark " + ({ TRUE: "yes", FALSE: "no" }[t] || "unknown")), t,
            reason ? el("small", "", reason) : null));
      }))),
      criteria(root),
      revisionCard(answer, at, root)),
    el("section", "panel changed", el("h2", "", "What changed"),
      ...group("Added", "green", groups.added), ...group("Removed", "red", groups.removed), ...group("Revised", "blue", groups.changed))));
}
