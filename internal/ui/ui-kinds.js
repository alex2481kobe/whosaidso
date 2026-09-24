// The detail screens of claims, decisions, instruments and sources, in the
// frame ui-detail.js draws. A claim adds show --stale's "measured code
// changed", its criterion from its history, its runs and every change.
import { el, mono, link, icon, actorName, actorReason, status, stamp, when, titleCase } from "./ui.js";
import { frame, panel, span } from "./ui-detail.js";

const ops = { eq: "=", ne: "≠", lt: "<", le: "≤", gt: ">", ge: "≥" };
const who = (a) => el("span", "", actorName(a), actorReason(a) ? el("small", "", " " + actorReason(a)) : null);
const short = (commit) => (commit ? commit.slice(0, 7) : "UNKNOWN");

// criterion is the claim's newest fixed criterion, as its history records it.
function criterion(history) {
  const fixes = history.events.filter((e) => e.event.type === "criterion.fix");
  const e = fixes[fixes.length - 1]?.event.data.expression;
  if (!e) return "none fixed";
  const target = e.target?.number ?? e.target?.string ?? e.target?.boolean ?? JSON.stringify(e.target);
  return `${ops[e.operator] || e.operator} ${target}${e.unit ? " " + e.unit : ""}`;
}

// expander is a line with a button that shows its detail inline.
function expander(time, title, quote, detail, label = "View diff") {
  detail.hidden = true;
  const button = el("button", "button", label);
  button.type = "button";
  button.addEventListener("click", () => { detail.hidden = !detail.hidden; button.textContent = detail.hidden ? label : "Hide"; });
  return el("li", "", el("span", "tl-dot"), el("span", "tl-time mono", time),
    el("div", "", el("strong", "", title), quote ? el("p", "quote", quote) : null, detail), button);
}

// changes lists every amendment of the record and, for a claim, every code
// change a proof set a failing run aside for, oldest first.
function changes(answer, at, proofs = []) {
  const rows = answer.amendments.map((a) => [a.origin, expander(when(at(a.origin)), `Revision ${a.revision.revision}`,
    a.review?.reason ? `“${a.review.reason}” — ${actorName(a.review.actor)}` : "",
    el("ul", "change-list", a.changes.map((c) => el("li", "", mono(c.path), ` ${c.change}`))))]);
  for (const p of proofs) for (const ev of p.admission.evidence) {
    const cc = ev.code_change;
    if (!cc) continue;
    rows.push([p.origin, expander(when(at(p.origin)), `Code updated from ${short(cc.from.commit)} → ${short(cc.to.commit)}`,
      `“${p.admission.judgment.reason}” — ${actorName(p.admission.judgment.actor)}`,
      el("ul", "change-list", cc.changed_paths.map((path) => el("li", "", mono(path)))))]);
  }
  rows.sort((a, b) => a[0].sequence - b[0].sequence || a[0].event_index - b[0].event_index);
  return panel("Changes over time", rows.length ? el("ol", "timeline changes", rows.map((r) => r[1])) : el("p", "none", "none"));
}

export function renderClaim({ id, root, answer, history, at }, stale) {
  const claim = root.claim;
  const [label, tone] = status(root);
  const st = stale.stale?.claims?.find((c) => c.claim.record_id === id);
  const changed = !st ? el("span", "", "no run to compare")
    : st.stale === "TRUE" ? el("span", "tone-text red", "Yes ", el("span", "alert", "!"))
      : st.stale === "FALSE" ? el("span", "tone-text green", "No") : el("span", "", "UNKNOWN", el("small", "", " " + (st.reason || "")));
  const banner = st?.stale === "TRUE" ? el("div", "banner", icon("warn"),
    el("p", "", `The measured code has changed since the last run (${st.changed_paths.join(", ")}: ${short(st.run_head?.commit)} → ${short(st.current_head?.commit)}).`,
      el("br"), "This claim may need to be re-measured.")) : null;
  const proofs = claim.proofs || [];
  const dispositions = new Map();
  for (const p of proofs) for (const ev of p.admission.evidence) dispositions.set(ev.invocation_ref.invocation_id, ev.disposition);
  const toneOf = { supports: "green", contradicts: "red" };
  const rows = answer.runs.map((r) => {
    const head = r.start?.execution_source_identity?.head;
    const dirty = r.start?.execution_source_identity?.dirty;
    const d = dispositions.get(r.invocation);
    const outcome = r.outcome?.kind === "exit" ? `exit ${r.outcome.exit_code}` : r.sealed ? titleCase(r.outcome?.kind || "UNKNOWN") : "unsealed";
    return el("tr", "", el("td", "", mono(r.invocation, "strong")), el("td", "", outcome),
      el("td", "tone-text " + (toneOf[d] || "muted"), d ? titleCase(d) : "not in a proof"),
      el("td", "", head?.state === "known" ? mono(short(head.value.commit) + (dirty?.value ? " dirty" : "")) : "UNKNOWN"),
      el("td", "", stamp(r.started_at ? new Date(r.started_at) : null)));
  });
  return frame({ id, label: root.label, tone, statusWord: "Verdict", statusLabel: label, sentence: claim.standing,
    command: `whosaidso continue ${id}`, meta: [["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)],
      ["Criterion", criterion(history)], ["Verdict", el("span", "tone-text " + tone, label)], ["Measured code changed?", changed]] },
  banner,
  el("section", "runs", el("h2", "", "Measurement runs"), el("table", "records",
    el("thead", "", el("tr", "", ["Run id", "Outcome", "In the proof", "Code version", "Date"].map((h) => el("th", "", h)))),
    el("tbody", "", rows.length ? rows : el("tr", "", el("td", "none", "none"))))),
  changes(answer, at, proofs));
}

export function renderDecision({ id, root, answer, at, dates }) {
  const d = root.decision;
  const [label, tone] = status(root);
  return frame({ id, label: root.label, tone, statusWord: "Status", statusLabel: label, sentence: d.authorizes || d.question,
    command: `whosaidso continue ${id}`, meta: [["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)],
      ["Waiting on", who(d.waiting_actor)], ["Created", mono(stamp(dates[0]))], ["Updated", mono(stamp(dates[1]))]] },
  el("div", "two-col",
    panel("Options", el("ul", "dots", (d.options.length ? d.options : ["none"]).map((o) => el("li", "", o)))),
    panel("Rulings", d.rulings.length ? el("ul", "dots", d.rulings.map((r) => el("li", "", JSON.stringify(r)))) : el("p", "none", "none"))),
  changes(answer, at));
}

export function renderInstrument({ id, root, answer, at, dates }) {
  const i = root.instrument;
  const [label, tone] = status(root);
  const v = i.validation;
  const list = (xs) => (xs.length ? xs.join("; ") : "none");
  return frame({ id, label: root.label, tone, statusWord: "Trust", statusLabel: label, sentence: i.question_answered,
    command: `whosaidso continue ${id}`, meta: [["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)],
      ["Validation", v.state === "KNOWN" ? `KNOWN, version ${v.version}` : el("span", "", "UNKNOWN", el("small", "", " " + v.reason))],
      ["Valid range", i.valid_range], ["Created", mono(stamp(dates[0]))]] },
  panel("What it measures", el("dl", "facts", [["Answers", i.question_answered], ["Blind to", i.blind_to], ["Does not answer", i.not_answered],
    ["Config surface", list(i.config_surface)], ["Dangerous defaults", list(i.dangerous_defaults)],
    ["Implementation", i.implementation_ref?.content?.locators?.[0]?.path || i.implementation_ref?.git?.path || i.implementation_ref?.kind]]
    .map(([k, val]) => el("div", "", el("dt", "", k), el("dd", "", val))))),
  changes(answer, at));
}

export function renderSource(source, history, at) {
  const s = source.intake;
  const dates = span(history, at);
  return frame({ id: s.source_id, label: `${actorName(s.speaker)} said, ${s.length} bytes`, tone: "green", statusWord: "Status",
    statusLabel: "Captured", sentence: "A source records only what was said; nothing in it becomes a fact by being captured.",
    command: `whosaidso show ${s.source_id}`, meta: [["Captured by", who(source.author?.actor)], ["Approved by", who(source.admitted_by)],
      ["Speaker", who(s.speaker)], ["Order", String(s.order)], ["Created", mono(stamp(dates[0]))]] },
  el("div", "two-col",
    panel("Concerns", el("ul", "dots", (s.referents.length ? s.referents : [null]).map((r) => el("li", "", r ? link(r.record_id, `${r.record_id} rev ${r.revision}`, "mono link") : "none")))),
    panel("Bytes", el("dl", "facts", [["Digest", mono(s.original_digest)], ["Length", String(s.length)],
      ["Media type", s.source_ref?.content?.media_type || "UNKNOWN"]].map(([k, v]) => el("div", "", el("dt", "", k), el("dd", "", v)))))));
}
