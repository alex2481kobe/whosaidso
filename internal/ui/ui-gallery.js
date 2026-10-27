// Gallery presentation: artwork and titles, with provenance in artifact details.
// File availability never implies approval. Mutations retain the original server contract.
import { el, api, apiURL, send, project, stamp, icon, link } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { action, preview, fileURL, saveOriginal } from "./ui-gallery-media.js";
import { inspect } from "./ui-gallery-inspect.js";
let renderEpoch = 0;
const state = { project: "", search: "", kind: "", sort: "recent", offset: 0 };
const kinds = [["", "All types"], ["image", "Images"], ["video", "Videos"], ["text", "Text"], ["other", "Other"]];
const sorts = [["recent", "Newest first"], ["oldest", "Oldest first"], ["title", "Title A–Z"], ["kind", "Kind"]];

function card(entry, titles, sequence) {
  const src = fileURL(entry);
  const media = preview(entry, entry.kind === "image" ? fileURL(entry, "thumb") : src);
  const stage = el("div", "artifact-preview", media);
  const open = trigger => inspect(entry, trigger, titles(), sequence());
  stage.tabIndex = 0;
  stage.setAttribute("role", "button");
  stage.setAttribute("aria-label", "Open " + entry.title);
  stage.addEventListener("click", e => { if (!e.target.closest("button")) open(stage); });
  stage.addEventListener("keydown", e => { if (e.target === stage && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); open(stage); } });
  if (entry.kind === "text") media.removeAttribute("tabindex");
  const title = action("open", entry.title, () => open(title), "artifact-title-button");
  title.setAttribute("aria-label", "Open " + entry.title + " details");
  const node = el("article", "gallery-card", stage, el("div", "gallery-card-body", el("h2", "gallery-title", title), saveOriginal(entry)));
  node.dataset.kind = entry.kind || "other";
  return node;
}
// The server filters the complete gallery, then observes only one page. Polls
// retain unchanged media and cannot overwrite a newer search or page request.
export async function renderGallery() {
  const epoch = ++renderEpoch;
  const shown = project();
  const active = () => epoch === renderEpoch && project() === shown;
  if (state.project !== shown) Object.assign(state, {project:shown, search:"", kind:"", offset:0});
  const params = () => ({project:shown, offset:state.offset, search:state.search, kind:state.kind, sort:state.sort});
  let answer = await api("/api/gallery", params());
  if (!active()) return el("div");
  let taskTitles = new Map();
  const empty = el("div", "gallery-empty", icon("file"), el("h2", "", "No artifacts here"),
    el("p", "", "Try another filter, or ask your agent to add a file to the gallery."));
  const cards = el("div", "gallery-cards");
  const tools = el("div", "gallery-tools");
  const said = el("p", "gallery-said");
  said.setAttribute("role", "status");
  const range = el("span", "gallery-footnote");
  const previous = action("back", "Previous", () => { if (answer.previous != null) refresh(answer.previous); });
  const next = action("arrow", "Next", () => { if (answer.next != null) refresh(answer.next); });
  const pager = el("nav", "gallery-tools", previous, range, next);
  pager.setAttribute("aria-label", "Gallery pages");
  const drawn = new Map();
  const draw = (page) => {
    answer = page;
    state.offset = page.offset;
    taskTitles = new Map(Object.entries(page.task_titles || {}));
    const nodes = page.entries.map(entry => {
      const sig = JSON.stringify(entry), old = drawn.get(entry.id);
      if (old?.sig === sig) return old.node;
      const node = card(entry, () => taskTitles, () => answer.entries);
      drawn.set(entry.id, {sig, node});
      return node;
    });
    const keep = new Set(nodes);
    for (const child of [...cards.children]) if (!keep.has(child)) child.remove();
    const live = new Set(page.entries.map(entry => entry.id));
    for (const id of drawn.keys()) if (!live.has(id)) drawn.delete(id);
    let at = cards.firstChild;
    for (const node of nodes) {
      if (node === at) at = at.nextSibling;
      else cards.insertBefore(node, at);
    }
    empty.hidden = page.entries.length > 0;
    previous.disabled = page.previous == null;
    next.disabled = page.next == null;
    range.textContent = page.total ? `${page.offset + 1}–${page.offset + page.entries.length} of ${page.total}` : "0 files";
  };
  let generation = 0, busy = false, current = null, debounce = null;
  async function refresh(offset = state.offset) {
    if (!active()) return;
    state.offset = offset;
    const mine = ++generation;
    current?.abort();
    const stop = new AbortController();
    current = stop;
    busy = true;
    const limit = setTimeout(() => stop.abort(), 10000);
    try {
      const page = await api("/api/gallery", params(), stop.signal);
      if (!active() || mine !== generation || !cards.isConnected) return;
      draw(page);
      said.textContent = "";
    } catch (error) {
      if (active() && mine === generation && cards.isConnected)
        said.textContent = error.name === "AbortError" ? "Gallery refresh timed out. Retry or wait for the next refresh." : error.message;
    } finally {
      clearTimeout(limit);
      if (mine === generation) { busy = false; current = null; }
    }
  }
  const reload = () => {
    generation++;
    current?.abort();
    clearTimeout(debounce);
    busy = true;
    state.offset = 0;
    debounce = setTimeout(() => refresh(0), 250);
  };
  const change = values => { if (!active()) return; Object.assign(state, values); reload(); };
  const paint = () => tools.replaceChildren(
    dropdown({prefix:"", options:kinds, value:state.kind, onPick:value => { change({kind:value}); paint(); }}),
    dropdown({glyph:"sort", prefix:"", options:sorts, value:state.sort, onPick:value => { change({sort:value}); paint(); }}));
  const search = el("input", "search-input");
  Object.assign(search, {type:"search", placeholder:"Search artifacts…", value:state.search, spellcheck:false});
  search.setAttribute("aria-label", "Search artifacts");
  search.addEventListener("input", () => { change({search:search.value.trim()}); });
  paint();
  draw(answer);
  const timer = setInterval(() => {
    if (!active() || !cards.isConnected) {
      generation++;
      current?.abort();
      clearTimeout(debounce);
      clearInterval(timer);
      return;
    }
    if (!busy && document.visibilityState !== "hidden") refresh();
  }, 3000);
  return el("div", "screen gallery-screen", el("header", "gallery-heading", el("h1", "", "Gallery")),
    el("div", "gallery-toolbar", el("div", "gallery-search-tools", el("label", "search", icon("search"), search), tools)),
    said, cards, empty, pager);
}
