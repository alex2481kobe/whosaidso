// History: the history view's events newest first, one table row each: when,
// what happened in plain words, the record it names and who proposed it, one
// line each. An opened row shows its title and proposer whole, then who
// proposed and who approved it, the review's reason and the record, with the
// values under "What happened". A bundle's review sits inside the rows it admitted; a review
// with nothing admitted (a rejection, a correction request) is its own row.
import { el, view, times, titles, when, actorName, copyButton, openButton, facts, disclose, icon } from "./ui.js";
import { packetTitle } from "./ui-home.js";

const happened = {
  "task.create": "Task created", "task.amend": "Task changed", "task.start": "Work started", "attempt.terminal": "Handed back",
  "task.close": "Task closed", "blocker.hold": "Put on hold", "blocker.clear": "Hold cleared", "claim.assert": "Claim made",
  "claim.revise": "Claim changed", "criterion.fix": "Criterion set", "invocation.start": "Run started", "invocation.seal": "Run finished",
  "proof.admit": "Proof recorded", "decision.open": "Decision opened", "decision.revise": "Decision changed", "decision.dispose": "Decision made",
  "instrument.declare": "Instrument added", "instrument.revise": "Instrument changed", "trust.withdraw": "Trust withdrawn",
  "source.intake": "Source captured", supersede: "Superseded", correction: "Corrected", "artifact.dispose": "Evidence disposed",
};
const reviewed = { rejected: "Proposal rejected", "correction-requested": "Correction requested" };

// recordOf is the record id an event names, read from its own fields.
function recordOf(data) {
  return data.id || data.source_id || data.target?.record_id || data.task?.record_id || data.claim?.record_id
    || data.decision?.record_id || data.instrument?.record_id || data.prior?.record_id
    || data.envelope?.criterion_ref?.value?.claim?.record_id || data.hold?.task?.record_id || "";
}

export async function renderHistory() {
  const [answer, at, names, todo] = await Promise.all([view("history"), times(), titles(), view("todo")]);
  const proposals = new Map(todo.packets_not_accepted.map((p) => [p.command_id, packetTitle(p)]));
  // Every bundle's review gives its reason; a bundle holding only a review
  // (a rejected or correction-requested packet) keeps its own row.
  const reviews = new Map();
  const others = new Set();
  for (const row of answer.events) {
    if (row.event.type === "review.admit") reviews.set(row.origin.sequence, row.event.data);
    else others.add(row.origin.sequence);
  }
  const rows = answer.events.filter((r) => r.event.type !== "review.admit" || !others.has(r.origin.sequence)).reverse().map((row) => {
    const data = row.event.data;
    const alone = row.event.type === "review.admit";
    const review = alone ? data : reviews.get(row.origin.sequence);
    const target = alone ? "" : recordOf(data);
    const packet = alone ? (data.packets || [])[0]?.command_id : "";
    const proposer = alone ? Object.values(data.authors || {})[0] : row.author?.actor;
    const what = alone ? reviewed[data.outcome] || "Reviewed: " + data.outcome : happened[row.event.type] || row.event.type;
    const title = target ? names.get(target) || target : proposals.get(packet) || "";
    const detail = el("td", "", facts([["Proposed by", actorName(proposer)], [alone ? "Reviewed by" : "Approved by", actorName(alone ? data.actor : row.admitter)],
      ["Reason", review?.reason || ""],
      ["Record", target ? el("span", "id-line", openButton(target), copyButton("Copy id", target), el("span", "mono muted", target)) : ""]], "facts wide"));
    detail.colSpan = 4;
    const cell = (className, text) => { const td = el("td", className, text); td.title = text; return td; };
    const tr = el("tr", "event", el("td", "time", el("span", "chev", icon("chevron")), when(at(row.origin))), cell("", what), cell("title-cell", title), cell("", actorName(proposer)));
    const more = el("tr", "event-detail", detail);
    const body = el("tbody", "", tr, more);
    disclose(tr, more, body);
    return body;
  });
  const table = el("table", "table history",
    el("thead", "", el("tr", "", ["Time", "What happened", "Record", "By"].map((h) => el("th", "", h)))), rows);
  return el("div", "screen fill",
    answer.result === "UNKNOWN" ? el("p", "notice", "UNKNOWN: " + answer.reason) : null,
    el("div", "table-panel", table, rows.length ? null : el("p", "empty", "empty...")));
}
