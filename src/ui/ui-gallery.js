// The Gallery screen: a grid of evidence with search, a type filter and a sort.
import { el, state, api, searchBox } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { card, message } from "./ui-components.js";
import { preview } from "./ui-gallery-media.js";
import { titleButton } from "./ui-gallery-actions.js";
import { inspect } from "./ui-gallery-inspect.js";
let filters = {project:"", search:"", kind:"", sort:"recent"};
function galleryCard(entry) {
  const stage = el("div", "artifact-preview", preview(entry));
  stage.tabIndex = 0; stage.setAttribute("role", "button"); stage.setAttribute("aria-label", "Open " + entry.shows);
  stage.addEventListener("click", e => { if (!e.target.closest("button,video")) inspect(entry, stage); });
  stage.addEventListener("keydown", e => { if (e.target === stage && ["Enter"," "].includes(e.key)) { e.preventDefault(); inspect(entry, stage); } });
  const node = card(stage, el("div", "gallery-card-body", el("h2", "gallery-title", titleButton(entry))));
  node.classList.add("gallery-card"); node.dataset.kind = entry.kind; return node;
}
export function galleryScreen() {
  if (filters.project !== state.project) filters = {project: state.project, search: "", kind: "", sort: filters.sort};
  const requested = new URLSearchParams(location.hash.split("?")[1] || "").get("item");
  if (requested) { filters.search = ""; filters.kind = ""; }
  const selected = filters, nodes = new Map();
  let data, shown = "", opened = false;
  const cards = el("div", "gallery-cards"), tools = el("div", "gallery-tools"), archived = el("section", "card archives");
  const empty = message("None");
  const render = () => {
    const entries = data.entries.filter(e => (!selected.kind || e.kind === selected.kind) && JSON.stringify(e).toLowerCase().includes(selected.search));
    if (selected.sort === "oldest") entries.reverse();
    // Cards already on screen stay as they are, so a playing video survives an update.
    const key = entries.map(e => e.id + e.status).join();
    if (key !== shown) {
      shown = key;
      cards.replaceChildren(...entries.map(e => { const id = e.id + e.status; if (!nodes.has(id)) nodes.set(id, galleryCard(e)); return nodes.get(id); }));
    }
    empty.hidden = entries.length > 0;
    const index = entries.findIndex(e => e.id === requested), trigger = cards.children[index]?.querySelector(".artifact-preview");
    if (trigger && !opened) requestAnimationFrame(() => {
      if (trigger.isConnected && !opened) { opened = true; inspect(entries[index], trigger); }
    });
    archived.replaceChildren(el("h2", "", "Archived"), ...data.archives.map(a =>
      el("div", "archive-row", el("strong", "", a.month), el("span", "muted", a.count))));
    archived.hidden = !data.archives.length;
  };
  const paint = () => tools.replaceChildren(
    dropdown({options:[["","All"],["image","Images"],["video","Videos"],["document","PDFs"]],value:selected.kind,onPick:value => {selected.kind = value; paint(); render();}}),
    dropdown({glyph:"sort",options:[["recent","Newest"],["oldest","Oldest"]],value:selected.sort,onPick:value => {selected.sort = value; paint(); render();}}));
  paint();
  const node = el("div", "screen", el("div", "toolbar", searchBox("Search", value => {selected.search = value; render();}, selected.search), tools), cards, empty, archived);
  return { node, async load() { data = await api("/api/gallery"); render(); return data.version; } };
}
