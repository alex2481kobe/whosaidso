// Numbers and rows derived in the browser from the views' own JSON: the
// history's events as rows people read, when each record was first and last
// named by an event, per-day counts for sparklines and "this week" counts.
// Every number here is counted from rows the ledger returned; nothing is
// estimated, and an event whose capture time is unknown is counted in no day.
import { view, times, titles, actorName } from "./ui.js";
import { happened, groupOf } from "./ui-icons.js";

const reviewed = { rejected: "Proposal rejected", "correction-requested": "Correction requested" };
export const DAY = 864e5;

// recordOf is the record id an event names, read from its own fields.
export function recordOf(data) {
  return data.id || data.source_id || data.target?.record_id || data.task?.record_id || data.claim?.record_id
    || data.decision?.record_id || data.instrument?.record_id || data.prior?.record_id
    || data.envelope?.criterion_ref?.value?.claim?.record_id || data.hold?.task?.record_id || "";
}

// packetTitle names a proposal by what its first event proposes.
export function packetTitle(packet) {
  const events = packet.packet?.events || [];
  const spec = events[0]?.data?.spec || {};
  const words = spec.intent || spec.question || spec.assertion || spec.question_answered || events[0]?.type || "Empty proposal";
  return events.length > 1 ? `${words} (+${events.length - 1} more)` : words;
}

// activity is the history as rows, oldest first. A bundle's review sits in
// the rows it admitted; a review that admitted nothing (a rejection, a
// correction request) is its own row. created and updated are the first and
// the latest time an event named each record; captured is the capture time
// of every packet a review judged.
export async function activity() {
  const [answer, at, names, todo] = await Promise.all([view("history"), times(), titles(), view("todo")]);
  const proposals = new Map(todo.packets_not_accepted.map((p) => [p.command_id, packetTitle(p)]));
  const reviews = new Map();
  const others = new Set();
  const packets = new Map();
  for (const row of answer.events) {
    if (row.event.type !== "review.admit") { others.add(row.origin.sequence); continue; }
    reviews.set(row.origin.sequence, row.event.data);
    for (const [id, stamp] of Object.entries(row.event.data.captured_at || {})) packets.set(id, stamp?.state === "known" ? new Date(stamp.value) : null);
  }
  const created = new Map();
  const updated = new Map();
  const rows = answer.events.filter((r) => r.event.type !== "review.admit" || !others.has(r.origin.sequence)).map((row, index) => {
    const data = row.event.data;
    const alone = row.event.type === "review.admit";
    const review = alone ? data : reviews.get(row.origin.sequence);
    const target = alone ? "" : recordOf(data);
    const packet = alone ? (data.packets || [])[0]?.command_id : "";
    const time = at(row.origin);
    if (target && time) {
      if (!created.has(target)) created.set(target, time);
      updated.set(target, time);
    }
    return {
      index, time, type: row.event.type, alone, target, group: groupOf(row.event.type),
      what: alone ? reviewed[data.outcome] || "Reviewed: " + data.outcome : happened[row.event.type] || row.event.type,
      title: target ? names.get(target) || target : proposals.get(packet) || "",
      proposer: actorName(alone ? Object.values(data.authors || {})[0] : row.author?.actor),
      approver: actorName(alone ? data.actor : row.admitter), approverLabel: alone ? "Reviewed by" : "Approved by",
      reason: review?.reason || "",
    };
  });
  // captured is when each reviewed packet was captured, one date per packet
  return { rows, created, updated, names, at, captured: [...packets.values()], result: answer.result, reason: answer.reason };
}

// startOfDay is local midnight of a date.
export function startOfDay(d) {
  const x = new Date(d);
  x.setHours(0, 0, 0, 0);
  return x;
}

// perDay counts dates into the last n local days, today last.
export function perDay(dates, n = 14) {
  const today = startOfDay(new Date()).getTime();
  const counts = new Array(n).fill(0);
  for (const d of dates) {
    if (!d) continue;
    const i = n - 1 - Math.round((today - startOfDay(d).getTime()) / DAY);
    if (i >= 0 && i < n) counts[i]++;
  }
  return counts;
}

// within is true for a known date inside the last n days (rolling).
export const within = (d, n) => Boolean(d) && Date.now() - d.getTime() < n * DAY && d.getTime() <= Date.now() + 60e3;

// count is how many of xs pass test.
export const count = (xs, test) => xs.reduce((n, x) => n + (test(x) ? 1 : 0), 0);
