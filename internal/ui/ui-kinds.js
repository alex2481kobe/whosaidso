// The detail screens of claims, decisions, instruments and sources, in the
// frame ui-detail.js draws. A claim adds show --stale's code-changed chip,
// its criterion from its history, its runs, its proofs and every change.
import { el, link, facts, item, section, disclose, copyButton, mono, icon, actorName, status, stamp, when, titleCase } from "./ui.js";
import { frame, panel, span, changes, short } from "./ui-detail.js";

const ops = { eq: "=", ne: "≠", lt: "<", le: "≤", gt: ">", ge: "≥" };
const who = (a) => actorName(a) + (a?.reason || a?.unknown_reason ? ": " + (a.reason || a.unknown_reason) : "");

// criterion is the claim's newest fixed criterion, as its history records it.
function criterion(history) {
  const fixes = history.events.filter((e) => e.event.type === "criterion.fix");
  const e = fixes[fixes.length - 1]?.event.data.expression;
  if (!e) return "none fixed";
  const target = e.target?.number ?? e.target?.string ?? e.target?.boolean ?? JSON.stringify(e.target);
  return `${ops[e.operator] || e.operator} ${target}${e.unit ? " " + e.unit : ""}`;
}

// staleChip is show --stale's answer for the claim as one small chip that
// opens its detail; nothing when the code is unchanged or never ran.
function staleChip(st) {
  if (!st || st.stale === "FALSE") return null;
  const changed = st.stale === "TRUE";
  const button = el("button", "chip-flag " + (changed ? "red" : "muted"), changed ? "Code changed since last run" : "Code change since last run UNKNOWN", icon("chevron"));
  button.type = "button";
  const detail = el("div", "chip-detail", facts(changed ? [["Changed paths", (st.changed_paths || []).join(", ")],
    ["Last run on", short(st.run_head?.commit)], ["Now at", short(st.current_head?.commit)], ["Meaning", "this claim may need to be measured again"]]
    : [["Reason", st.reason || "no reason recorded"]]));
  const holder = el("div", "");
  disclose(button, detail, holder);
  return [button, detail];
}

export function renderClaim({ id, root, answer, history, at, names }, stale) {
  const claim = root.claim;
  const [label, tone] = status(root);
  const proofs = claim.proofs || [];
  const dispositions = new Map();
  for (const p of proofs) for (const ev of p.admission.evidence) dispositions.set(ev.invocation_ref.invocation_id, ev.disposition);
  const toneOf = { supports: "green", contradicts: "red" };
  const runs = answer.runs.map((r) => {
    const head = r.start?.execution_source_identity?.head;
    const dirty = r.start?.execution_source_identity?.dirty;
    const code = head?.state === "known" ? short(head.value.commit) + (dirty?.value ? " dirty" : "") : "UNKNOWN";
    const d = dispositions.get(r.invocation);
    const outcome = r.outcome?.kind === "exit" ? `Exit ${r.outcome.exit_code}` : r.sealed ? titleCase(r.outcome?.kind || "UNKNOWN") : "Unsealed";
    return item(`${outcome}, code ${code}`, d ? titleCase(d) : "Not in a proof", facts([["Run id", el("span", "id-line", mono(r.invocation), copyButton("Copy id", r.invocation))],
      ["Outcome", outcome], ["Code version", code], ["Started", stamp(r.started_at ? new Date(r.started_at) : null)]]), toneOf[d] || "muted");
  });
  const proofRows = proofs.map((p) => item(`Proof ${p.admission.verdict || "UNKNOWN"} the claim`, when(at(p.origin)),
    facts([["Judged by", actorName(p.admission.judgment?.actor)], ["Reason", p.admission.judgment?.reason],
      ["Evidence", el("ul", "plain", p.admission.evidence.map((ev) => el("li", "", `${titleCase(ev.disposition)}: ${ev.reason || "no reason recorded"}`)))]])));
  return frame({ id, title: root.label, tone, statusLabel: label, chip: staleChip(stale.stale?.claims?.find((c) => c.claim.record_id === id)),
    details: [["Standing", claim.standing], ["Criterion", criterion(history)], ["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)]] },
  section("Measurement runs", runs), section("Proofs", proofRows), changes(answer, at, names, proofs));
}

export function renderDecision({ id, root, answer, at, names, dates }) {
  const d = root.decision;
  const [label, tone] = status(root);
  return frame({ id, title: root.label, tone, statusLabel: label, details: [["Question", d.question], ["Authorizes", d.authorizes],
    ["Waiting on", who(d.waiting_actor)], ["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)], ["Created", stamp(dates[0])], ["Updated", stamp(dates[1])]] },
  section("Options", d.options.map((o) => item(o, "", el("p", "", o)))),
  section("Rulings", d.rulings.map((r) => item(titleCase(r.disposition.disposition), when(at(r.origin)),
    facts([["Quote", r.disposition.quote], ["Ruled by", actorName(r.author?.actor)]])))),
  changes(answer, at, names));
}

export function renderInstrument({ id, root, answer, at, names, dates }) {
  const i = root.instrument;
  const [label, tone] = status(root);
  const v = i.validation;
  const joined = (xs) => (xs.length ? xs.join("; ") : "none");
  return frame({ id, title: root.label, tone, statusLabel: label, details: [["Validation", v.state === "KNOWN" ? `Version ${v.version}` : "UNKNOWN: " + v.reason],
    ["Valid range", i.valid_range], ["Author", who(root.author?.actor)], ["Approved by", who(root.admitted_by)], ["Created", stamp(dates[0])]] },
  panel("What it measures", facts([["Answers", i.question_answered], ["Blind to", i.blind_to], ["Does not answer", i.not_answered],
    ["Config surface", joined(i.config_surface)], ["Dangerous defaults", joined(i.dangerous_defaults)],
    ["Implementation", i.implementation_ref?.content?.locators?.[0]?.path || i.implementation_ref?.git?.path || i.implementation_ref?.kind]])),
  section("Trust withdrawals", i.withdrawals.map((w) => item("Trust withdrawn", when(at(w.origin)), facts([["Revalidate when", w.withdrawal.revalidation_condition]])))),
  changes(answer, at, names));
}

export function renderSource(source, history, at, names) {
  const s = source.intake;
  const dates = span(history, at);
  return frame({ id: s.source_id, title: `${actorName(s.speaker)} said, ${s.length} bytes`, tone: "green", statusLabel: "Captured",
    details: [["Speaker", who(s.speaker)], ["Captured by", who(source.author?.actor)], ["Approved by", who(source.admitted_by)], ["Order", String(s.order)], ["Created", stamp(dates[0])]] },
  section("Concerns", s.referents.map((r) => el("li", "item", link(r.record_id, names.get(r.record_id) || r.record_id, "item-link")))),
  panel("Bytes", facts([["Digest", mono(s.original_digest)], ["Length", String(s.length)], ["Media type", s.source_ref?.content?.media_type || "UNKNOWN"]])));
}
