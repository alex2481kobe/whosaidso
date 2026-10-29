// The Goals screen: totals, one block per phase, one expandable row per goal.
import { el, api, apiURL, state, chip, mono, titleCase, ago, stamp, searchBox, icon } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { message } from "./ui-components.js";

const ORDER = ["done", "active", "failing", "landed", "stale", "idle", "open"];
// Filters and open rows outlive a re-render and a visit to the other screen, but not a project switch.
const kept = { project: "", search: "", state: "", open: new Set() };
const parts = counts => ORDER.filter(s => counts[s]).map(s => `${counts[s]} ${s}`);
const sum = counts => Object.values(counts).reduce((a, b) => a + b, 0);

// A long worktree path keeps its last segments; the whole path is the tooltip.
const tail = path => { const parts = path.split("/"); return parts.length > 4 ? "…/" + parts.slice(-3).join("/") : path; };
const field = (label, ...content) => el("div", "field", el("dt", "", label), el("dd", "", content));
// The claim folds into the branch line it was made from (the same branch, locally or as a remote's copy, in the
// same worktree or one this clone cannot see); a claim from anywhere else gets its own line.
const claimedBy = (b, c) => (b.name === c.branch || b.name.endsWith("/" + c.branch)) && (!b.worktree || b.worktree === c.path);
function work(g) {
  const line = (label, name, who, time, path) => el("div", "work", label && el("b", "", label), mono(name), el("span", "muted", `${who} · ${ago(time)}`), path && Object.assign(el("span", "mono muted path", tail(path)), {title: path}));
  const c = g.claim, held = c ? g.branches.find(b => claimedBy(b, c)) : null;
  return el("div", "works",
    g.branches.map(b => b === held ? line("claimed", b.name, b.author, b.time, b.worktree || c.path) : line("", b.name, b.author, b.time, b.worktree)),
    c && !held && line("claimed", c.branch, c.author, c.since, c.path));
}
function thumbs(media) {
  return el("div", "thumbs", media.map(m => {
    const a = el("a", "thumb", m.tag === "img" ? Object.assign(el("img"), {src:apiURL("/api/media/" + m.media_sha256), alt:m.shows, loading:"lazy"}) : icon("file"));
    a.href = "#/gallery?item=" + encodeURIComponent(m.id); a.title = m.shows; return a;
  }));
}
const ran = p => ` ${p.instrument} · exit ${p.exit_code} · ${stamp(p.ran_at)} · `;
function detail(g) {
  const p = g.proof, fail = p && p.current && p.exit_code !== 0;
  return el("dl", "goal-detail",
    field("Done when", g.done_when),
    g.instrument && field("Check", el("b", "", g.instrument.id), " · ", g.instrument.answers, " ", mono(g.instrument.run.join(" "))),
    field("Latest proof", !p ? "None recorded; result unknown"
      : !p.current ? ["No current proof; result unknown. Newest, not current: ", el("b", "muted", p.exit_code === 0 ? "Pass" : "Fail"), ran(p), mono(p.commit.slice(0, 7))]
      : [el("b", "verdict " + (fail ? "fail" : "pass"), fail ? "Fail" : "Pass"), ran(p), mono(p.commit.slice(0, 7))]),
    fail && p.output_tail && field("Output", el("pre", "output", p.output_tail)),
    g.asks.length > 0 && field("Asks", g.asks.map(a => el("div", "ask", a.said.split("\n")[0], el("span", "muted", " · " + stamp(a.ran_at))))),
    g.media.length > 0 && field("Media", thumbs(g.media)));
}
function row(g) {
  const d = el("details", "goal");
  d.dataset.id = g.id; d.open = kept.open.has(g.id);
  d.append(el("summary", "goal-head", chip(g.state), el("span", "goal-title", g.title), el("span", "mono muted goal-id", g.id), work(g)));
  const fill = () => { if (!d.querySelector(".goal-detail")) d.append(detail(g)); };
  if (d.open) fill();
  d.addEventListener("toggle", () => { if (d.open) { kept.open.add(g.id); fill(); } else kept.open.delete(g.id); });
  return d;
}
function phase(p, rows) {
  const total = sum(p.counts), done = p.counts.done || 0, fill = el("i");
  fill.style.width = total ? `${100 * done / total}%` : "0";
  const bar = el("div", "bar", fill);
  bar.setAttribute("role", "progressbar"); bar.setAttribute("aria-label", `${p.title}: ${done} of ${total} done`);
  bar.setAttribute("aria-valuemin", "0"); bar.setAttribute("aria-valuemax", String(total)); bar.setAttribute("aria-valuenow", String(done));
  return el("section", "phase", el("header", "phase-head", el("h2", "", p.title), bar, el("span", "muted", parts(p.counts).join(" · "))), rows.map(row));
}
export function goalsScreen() {
  if (kept.project !== state.project) Object.assign(kept, {project: state.project, search: "", state: ""}), kept.open.clear();
  let data;
  const totals = el("span", "totals"), list = el("div", "goals"), choice = el("span", "choice");
  const draw = () => {
    totals.textContent = [`${data.goals.length} goals`, ...parts(data.totals)].join(" · ");
    const rows = data.goals.filter(g => (!kept.state || g.state === kept.state) && (!kept.search || JSON.stringify(g).toLowerCase().includes(kept.search)));
    const focused = document.activeElement?.closest?.("details.goal")?.dataset.id;
    list.replaceChildren(...data.phases.map(p => { const mine = rows.filter(g => g.phase === p.id); return mine.length ? phase(p, mine) : null; }).filter(Boolean));
    if (!rows.length) list.append(message(data.goals.length ? "No goals match these filters." : "No goals yet."));
    if (focused) list.querySelector(`details.goal[data-id="${CSS.escape(focused)}"] summary`)?.focus({preventScroll: true});
  };
  const paint = () => choice.replaceChildren(dropdown({options: [["", "All states"], ...ORDER.map(s => [s, titleCase(s)])], value: kept.state, onPick: v => { kept.state = v; paint(); draw(); }}));
  paint();
  const node = el("div", "screen", el("div", "toolbar", totals, searchBox("Search goals…", v => { kept.search = v; draw(); }, kept.search), choice), list);
  return { node, async load() { data = await api("/api/goals"); draw(); return data.version; } };
}
