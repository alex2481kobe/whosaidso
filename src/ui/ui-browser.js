// Goals and Records retain the reference browser's controls and list-card layout.
import { el, live, link, mono, chip, searchBox, titleCase, stamp } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { listCard, listRow, message, iconButton } from "./ui-components.js";
import { phasePanel, goalRow } from "./ui-overview.js";
export async function renderBrowser(screen) {
  let search = "", picked = new URLSearchParams(location.hash.split("?")[1] || "").get("state") || "", data;
  const content = el("div", "live-content"), tools = el("div", "gallery-tools");
  const options = screen === "goals" ? [["","All states"], ...["done","active","landed","failing","stale","open","idle"].map(s => [s,titleCase(s)])]
    : [["","All kinds"], ...["phase","goal","instrument","proof","ask"].map(s => [s,titleCase(s) + "s"])];
  const matches = v => JSON.stringify(v).toLowerCase().includes(search);
  const render = () => {
    if (!data) return;
    if (screen === "goals") {
      const selected = data.goals.filter(g => (!picked || g.state === picked) && matches(g));
      content.replaceChildren(...data.phases.map(p => {
        const rows = selected.filter(g => g.phase === p.id).map(g => { const row = goalRow(g); row.classList.add("goal-row"); return row; });
        return rows.length ? phasePanel(p, rows) : null;
      }).filter(Boolean));
      if (!selected.length) content.append(message("No goals match these filters."));
    } else {
      const rows = data.records.filter(r => (!picked || r.kind === picked) && matches(r));
      const panel = listCard();
      for (const r of rows) {
        const title = r.title || r.said || r.answers || (r.kind === "proof" ? `${r.goal} · ${r.instrument} · exit ${r.exit_code}` : r.id);
        panel.append(listRow(link(r.id, title, "row-title", r.kind), el("span", "muted", titleCase(r.kind)), mono(r.id), el("span", "muted", r.ran_at == null ? "—" : stamp(r.ran_at))));
      }
      content.replaceChildren(rows.length ? panel : message("No records match these filters."));
    }
  };
  const paint = () => tools.replaceChildren(dropdown({options,value:picked,onPick:value => {picked = value; paint(); render();}}), iconButton("refresh", "Refresh", () => refresh()));
  const {node, refresh} = await live(screen === "goals" ? "/api/goals" : "/api/records", (_, answer) => { data = answer; render(); });
  node.replaceChildren(content); paint();
  return el("div", "screen browser-screen", el("h1", "", titleCase(screen)),
    el("div", "gallery-search-tools", searchBox(screen === "goals" ? "Search goals…" : "Search records…", value => {search = value; render();}), tools), node);
}
