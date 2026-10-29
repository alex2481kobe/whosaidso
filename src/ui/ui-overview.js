// Overview uses the reference's stat cards and list cards with Rust goal rollups.
import { el, live, link, chip, titleCase, branchWork } from "./ui.js";
import { listCard, listRow, message, iconButton } from "./ui-components.js";
export const goalRow = g => listRow(chip(g.state), link(g.id, g.title, "row-title"), el("span", "mono muted", g.id), g.branches.length ? branchWork(g.branches) : el("span", "muted", "—"));
export function phasePanel(p, rows) {
  const node = listCard(p.title);
  const progress = el("progress"); progress.value = p.done; progress.max = Math.max(1, p.total);
  progress.setAttribute("aria-label", `${p.title}: ${p.done} of ${p.total} done`);
  node.append(el("div", "phase-progress", progress, el("p", "", p.summary)), ...rows);
  return node;
}
export async function renderOverview() {
  const screen = el("div", "screen browser-screen overview-screen", el("h1", "", "Overview"));
  const {node, refresh} = await live("/api/overview", (node, data) => {
    const labels = ["done", "active", "landed", "failing", "stale", "open", ...(data.counts.idle > 0 ? ["idle"] : [])];
    const stats = el("div", "stats six", labels.map(name => {
      const n = data.counts[name];
      const card = el("a", "stat", el("span", "stat-value", n), el("span", "stat-label", titleCase(name)));
      card.href = "#/goals?state=" + name; return card;
    }));
    const phases = data.phases.map(p => phasePanel(p, []));
    const flight = listCard("Goals in flight");
    flight.append(...(data.in_flight.length ? data.in_flight.map(g => { const row = goalRow(g); row.classList.add("goal-row"); return row; }) : [message("No goals in flight.")]));
    node.replaceChildren(stats, ...phases, flight);
  });
  screen.append(el("div", "counts-row", el("span", "muted", "Progress from local main and recorded proofs"), iconButton("refresh", "Refresh", () => refresh())), node);
  return screen;
}
