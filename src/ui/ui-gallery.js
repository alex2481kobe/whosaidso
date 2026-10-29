// Gallery grid and toolbar ported from v0.2.1 internal/ui/ui-gallery.js.
import { el, live, searchBox, icon, project, api } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { card, message, iconButton, listCard } from "./ui-components.js";
import { preview } from "./ui-gallery-media.js";
import { galleryActions, titleButton } from "./ui-gallery-actions.js";
import { inspect } from "./ui-gallery-inspect.js";
let filters = {project:"", search:"", kind:"", sort:"recent"};
export function galleryCard(entry) {
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const stage = el("div", "artifact-preview", preview(entry));
  stage.tabIndex = 0; stage.setAttribute("role", "button"); stage.setAttribute("aria-label", "Open " + entry.shows);
  stage.addEventListener("click", e => { if (!e.target.closest("button,video")) inspect(entry, stage); });
  stage.addEventListener("keydown", e => { if (e.target === stage && ["Enter"," "].includes(e.key)) { e.preventDefault(); inspect(entry, stage); } });
  const node = card(stage, el("div", "gallery-card-body", el("h2", "gallery-title", titleButton(entry)), galleryActions(entry, said), said));
  node.classList.add("gallery-card"); node.dataset.kind = entry.kind; return node;
}
export async function renderGallery() {
  if (filters.project !== project()) filters = {project:project(), search:"", kind:"", sort:filters.sort};
  const params = new URLSearchParams(location.hash.split("?")[1] || ""), requested = params.get("item");
  if (requested) { filters.search = ""; filters.kind = ""; }
  let opened = false;
  const selected = filters; let data;
  const cards = el("div", "gallery-cards"), tools = el("div", "gallery-tools"), archived = listCard("Archived months");
  const empty = message("No media match these filters.");
  const render = () => {
    if (!data) return;
    const entries = data.entries.filter(e => (!selected.kind || e.kind === selected.kind) && JSON.stringify(e).toLowerCase().includes(selected.search));
    if (selected.sort === "oldest") entries.reverse();
    cards.replaceChildren(...entries.map(galleryCard)); empty.hidden = entries.length > 0;
    const index = entries.findIndex(e => e.id === requested), trigger = cards.children[index]?.querySelector(".artifact-preview");
    if (trigger && !opened) requestAnimationFrame(() => {
      if (trigger.isConnected && !opened) { opened = true; inspect(entries[index], trigger); }
    });
    archived.replaceChildren(el("h2", "", "Archived months"), ...data.archives.map(a =>
      el("div", "archive-row", icon("file"), el("strong", "", a.month), el("span", "muted", `${a.count} items`), el("span", "mono", a.path))));
    archived.hidden = !data.archives.length;
  };
  const {node, refresh} = await live("/api/gallery", (_, answer) => { data = answer; render(); });
  node.replaceChildren(cards, empty, archived);
  const paint = () => tools.replaceChildren(
    dropdown({options:[["","All types"],["image","Images"],["video","Videos"],["document","PDF documents"]],value:selected.kind,onPick:value => {selected.kind = value; paint(); render();}}),
    dropdown({glyph:"sort",options:[["recent","Newest first"],["oldest","Oldest first"]],value:selected.sort,onPick:value => {selected.sort = value; paint(); render();}}),
    iconButton("refresh", "Refresh", () => refresh()));
  paint();
  // Activity can name live evidence older than the Gallery's newest 100.
  if (requested && params.get("goal") && !data.entries.some(e => e.id === requested)) {
    const entry = (await api("/api/goal", {id:params.get("goal")})).media.find(e => e.id === requested);
    if (entry) requestAnimationFrame(() => {
      const trigger = tools.querySelector("button");
      if (trigger?.isConnected && !opened) { opened = true; inspect(entry, trigger); }
    });
  }
  return el("div", "screen gallery-screen", el("h1", "", "Gallery"), el("div", "gallery-toolbar", el("div", "gallery-search-tools",
    searchBox("Search files…", value => {selected.search = value; render();}, selected.search), tools)), node);
}
