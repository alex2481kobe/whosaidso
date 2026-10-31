// The Goals screen: totals, one block per milestone, one expandable row per goal.
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
function detail(g) {
  const p = g.proof, verdict = p?.verdict;
  return el("div", "goal-detail",
    el("div", "", g.check && [mono(g.check.join(" ")), " · "],
      p ? [el("b", "verdict " + verdict + (p.current ? "" : " old"), "ran " + (p.current ? "" : "old ") + verdict), " · ", mono(p.commit.slice(0, 7)), " · " + stamp(p.ran_at)] : "no proof"),
    p && p.main && el("div", "muted", p.main.verdict === "pass" ? "passed on main" : "failed on main", p.reason && ` · ${p.reason}`),
    verdict === "fail" && p.current && p.output_tail && el("pre", "output", p.output_tail),
    g.media.length > 0 && [el("span", "muted", "attached"), thumbs(g.media)]);
}
function row(g) {
  const d = el("details", "goal");
  d.dataset.id = g.id; d.open = kept.open.has(g.id);
  d.append(el("summary", "goal-head", chip(g.state), el("span", "goal-title", g.title, g.check_changed && " · check changed"), el("span", "mono muted goal-id", g.id), work(g)));
  const fill = () => { if (!d.querySelector(".goal-detail")) d.append(detail(g)); };
  if (d.open) fill();
  d.addEventListener("toggle", () => { if (d.open) { kept.open.add(g.id); fill(); } else kept.open.delete(g.id); });
  return d;
}
function milestone(p, rows) {
  const total = sum(p.counts), done = p.counts.done || 0, fill = el("i");
  fill.style.width = total ? `${100 * done / total}%` : "0";
  const bar = el("div", "bar", fill);
  bar.setAttribute("role", "progressbar"); bar.setAttribute("aria-label", `${p.title}: ${done} of ${total} done`);
  bar.setAttribute("aria-valuemin", "0"); bar.setAttribute("aria-valuemax", String(total)); bar.setAttribute("aria-valuenow", String(done));
  return el("section", "milestone", el("header", "milestone-head", el("h2", "", p.title), bar, el("span", "muted", `${done}/${total}`)), rows.map(row));
}
export function goalsScreen() {
  if (kept.project !== state.project) Object.assign(kept, {project: state.project, search: "", state: ""}), kept.open.clear();
  let data;
  const totals = el("span", "totals"), list = el("div", "goals"), choice = el("span", "choice");
  const draw = () => {
    totals.textContent = [`${data.goals.length} goals`, ...parts(data.totals)].join(" · ");
    const rows = data.goals.filter(g => (!kept.state || g.state === kept.state) && (!kept.search || JSON.stringify(g).toLowerCase().includes(kept.search)));
    const focused = document.activeElement?.closest?.("details.goal")?.dataset.id;
    list.replaceChildren(...data.milestones.map(p => { const mine = rows.filter(g => g.milestone === p.id); return mine.length ? milestone(p, mine) : null; }).filter(Boolean));
    if (!rows.length) list.append(message("None"));
    if (focused) list.querySelector(`details.goal[data-id="${CSS.escape(focused)}"] summary`)?.focus({preventScroll: true});
  };
  const paint = () => choice.replaceChildren(dropdown({options: [["", "All"], ...ORDER.map(s => [s, titleCase(s)])], value: kept.state, onPick: v => { kept.state = v; paint(); draw(); }}));
  paint();
  const node = el("div", "screen", el("div", "toolbar", totals, searchBox("Search", v => { kept.search = v; draw(); }, kept.search), choice), list);
  return { node, async load() { data = await api("/api/goals"); draw(); return data.version; } };
}
