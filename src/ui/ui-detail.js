// Record framing and disclosures ported from v0.2.1 internal/ui/ui-detail.js.
import { el, live, facts, item, copyButton, chip, mono, stamp, link, titleCase, branchWork } from "./ui.js";
import { card, message } from "./ui-components.js";
import { galleryCard } from "./ui-gallery.js";
let origin = "#/records";
window.addEventListener("hashchange", e => {
  const from = new URL(e.oldURL).hash;
  if (location.hash.startsWith("#/record/") && !from.startsWith("#/record/")) origin = from || "#/records";
});
const panel = (title, body) => card(el("h2", "", title), el("div", "panel-body", body));
function proofPanel(title, p) {
  if (!p) return panel(title, message("No proof recorded. Result unknown."));
  const output = el("pre", "proof-output", p.output_tail); output.tabIndex = 0; output.setAttribute("aria-label", "Proof output tail");
  return panel(title, [facts([["Result", p.exit_code === 0 ? "Pass" : "Fail"], ["Exit code", p.exit_code], ["Instrument", p.instrument], ["Commit", mono(p.commit)], ["Time", stamp(p.ran_at)]]), output]);
}
function goalDetail(g) {
  const back = el("a", "back", "← Back"); back.href = origin;
  const metadata = el("details", "record-metadata", el("summary", "", "Record details"),
    facts([["Id", mono(g.id)], ["Broader", link(g.broader, g.broader, "link", g.phase === g.broader ? "phase" : "goal")]]), copyButton("Copy id", g.id));
  const check = g.instrument;
  const instrument = check ? panel("Instrument", [facts([["Id", link(g.check, g.check, "link", "instrument")], ["Answers", check.answers], ["Run", mono(JSON.stringify(check.run))]]),
    el("ul", "items", check.gaps.map(gap => item(gap.what, gap.covered_by, facts([["Why", gap.why], ["Covered by", link(gap.covered_by, gap.covered_by, "link", "instrument")]]))))]) : null;
  const asks = panel("Asks", g.asks.length ? el("ul", "items", g.asks.map(a => item(a.said, stamp(a.ran_at), facts([["Said", a.said], ["Commit", mono(a.commit)], ["Time", stamp(a.ran_at)]])))) : message("No asks recorded."));
  const sections = [panel("Done when", el("p", "detail-subject", g.done_when)), instrument, proofPanel("Newest proof", g.newest_proof)];
  if (g.branches.length) sections.unshift(panel("Branch work", branchWork(g.branches)));
  if (g.proof && g.proof.sha !== g.newest_proof?.sha) sections.push(proofPanel("Proof used for state", g.proof));
  sections.push(asks, panel("Media", g.media.length ? el("div", "gallery-cards", g.media.map(galleryCard)) : message("No media recorded.")));
  return el("div", "screen detail", el("h1", "", g.title), el("header", "detail-head", back, chip(g.state), metadata), el("div", "sections", sections));
}
function recordDetail(value) {
  const back = el("a", "back", "← Back"); back.href = origin;
  const title = value.title || value.said || value.answers || (value.kind === "proof" ? `${value.goal} · ${value.instrument} · exit ${value.exit_code}` : value.id);
  const metadata = el("details", "record-metadata", el("summary", "", "Record details"),
    facts([["Kind", titleCase(value.kind)], ["Id", mono(value.id)]]), copyButton("Copy id", value.id));
  const sections = [];
  if (value.kind === "phase") sections.push(panel("Phase", facts([["Title", value.title], ["Order", value.order]])));
  if (value.kind === "instrument") {
    sections.push(panel("Instrument", facts([["Answers", value.answers], ["Run", mono(JSON.stringify(value.run))]])));
    sections.push(panel("Gaps", value.gaps.length ? el("ul", "items", value.gaps.map(g => item(g.what, g.covered_by, facts([["Why", g.why], ["Covered by", link(g.covered_by, g.covered_by, "link", "instrument")]])))) : message("No gaps recorded.")));
  }
  if (value.kind === "proof") sections.push(proofPanel("Proof", value));
  if (value.kind === "ask") sections.push(panel("Ask", el("p", "detail-subject", value.said)));
  if (value.goal) sections.push(panel("Recorded for", facts([["Goal", link(value.goal)], ["Commit", mono(value.commit)], ["Time", stamp(value.ran_at)]])));
  return el("div", "screen detail", el("h1", "", title), el("header", "detail-head", back, metadata), el("div", "sections", sections));
}
export async function renderDetail(kind, id) {
  const {node} = await live(kind === "goal" ? "/api/goal" : "/api/record", (node, value) => {
    node.replaceChildren(kind === "goal" ? goalDetail(value) : recordDetail(value));
  }, {id});
  return node;
}
