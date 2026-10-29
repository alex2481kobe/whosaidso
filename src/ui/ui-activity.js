// Activity uses the reference browser's list, filters and kind chips.
import { el, live, link, searchBox, titleCase, stamp } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { listCard, listRow, message, iconButton } from "./ui-components.js";
const colors = {commit:"blue", proof:"green", media:"purple", ask:"amber"};
function activityRow(entry) {
  let text;
  if (entry.kind === "commit") text = `${entry.sha} ${entry.subject} (${entry.branch}, ${entry.author})`;
  if (entry.kind === "proof") text = `${entry.instrument} · ${entry.exit_code === 0 ? "Pass" : "Fail"}`;
  if (entry.kind === "ask") text = entry.text;
  if (entry.kind === "media") {
    text = el("a", "link activity-text", entry.title);
    text.href = "#/gallery?" + new URLSearchParams({item:entry.media_id, goal:entry.goal});
  } else {
    const words = text;
    text = el("span", "activity-text", words); text.title = words;
  }
  const kind = el("span", "state-chip tone-tint " + colors[entry.kind], titleCase(entry.kind));
  const row = listRow(el("time", "muted", stamp(entry.time * 1e9)), kind, link(entry.goal), text);
  row.classList.add("activity-row");
  return row;
}
export async function renderActivity() {
  let search = "", kind = "", data;
  const rows = listCard(), tools = el("div", "gallery-tools"), count = el("span", "muted");
  const render = () => {
    if (!data) return;
    const entries = data.entries.filter(e => (!kind || e.kind === kind) && JSON.stringify(e).toLowerCase().includes(search));
    rows.replaceChildren(...(entries.length ? entries.map(activityRow) : [message("No activity matches these filters.")]));
    count.textContent = `${entries.length} of ${data.entries.length} recent entries · Newest first`;
  };
  const {node, refresh} = await live("/api/activity", (_, answer) => { data = answer; render(); }, {limit:500});
  node.replaceChildren(rows);
  const paint = () => tools.replaceChildren(dropdown({options:[["","All kinds"], ...Object.keys(colors).map(k => [k,titleCase(k)])],
    value:kind,onPick:value => {kind = value; paint(); render();}}), iconButton("refresh", "Refresh", () => refresh()));
  paint();
  return el("div", "screen browser-screen activity-screen", el("h1", "", "Activity"),
    el("div", "gallery-search-tools", searchBox("Search activity…", value => {search = value; render();}), tools), count, node);
}
