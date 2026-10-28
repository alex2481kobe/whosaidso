// Gallery batches append near the scroll boundary. Filters reset the stream;
// unchanged polls preserve media nodes. File actions and inspection live apart.
import { el, api, project, icon } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { preview, fileURL } from "./ui-gallery-media.js";
import { galleryActions, titleButton, iconAction } from "./ui-gallery-actions.js";
import { inspect } from "./ui-gallery-inspect.js";
let renderEpoch = 0;
const state = { project: "", search: "", kind: "", sort: "recent" };
const kinds = [["", "All types"], ["image", "Images"], ["video", "Videos"], ["text", "Text"], ["other", "Other"]];
const sorts = [["recent", "Newest first"], ["oldest", "Oldest first"], ["title", "Title A–Z"], ["kind", "Kind"]];

function card(entry, removed, sequence) {
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const media = preview(entry, fileURL(entry, entry.kind === "image" ? "thumb" : "file"));
  const stage = el("div", "artifact-preview", media);
  const open = () => inspect(entry, stage, new Map(), sequence(), removed);
  stage.tabIndex = 0; stage.setAttribute("role", "button");
  stage.setAttribute("aria-label", "Open " + entry.title);
  stage.addEventListener("click", e => { if (!e.target.closest("button")) open(); });
  stage.addEventListener("keydown", e => { if (e.target === stage && (e.key === "Enter" || e.key === " ")) { e.preventDefault(); open(); } });
  if (entry.kind === "text") media.removeAttribute("tabindex");
  const node = el("article", "gallery-card", stage, el("div", "gallery-card-body",
    el("h2", "gallery-title", titleButton(entry, said)), galleryActions(entry, said, removed), said));
  node.dataset.kind = entry.kind || "other";
  return node;
}
export async function renderGallery() {
  const epoch = ++renderEpoch, shown = project();
  const active = () => epoch === renderEpoch && project() === shown;
  if (state.project !== shown) Object.assign(state, {project:shown, search:"", kind:""});
  const params = offset => ({project:shown, offset, search:state.search, kind:state.kind, sort:state.sort});
  let answer = await api("/api/gallery", params(0));
  if (!active()) return el("div");
  const cards = el("div", "gallery-cards"), tools = el("div", "gallery-tools");
  const empty = el("p", "gallery-empty", "No files match these filters.");
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const sentinel = el("div", "gallery-sentinel");
  const retry = iconAction("refresh", "Retry loading files", () => refresh(nextOffset)); retry.hidden = true;
  const drawn = new Map();
  let nextOffset = answer.next, firstSignature = "", generation = 0, busy = false, polling = false, current, debounce;
  const signature = page => JSON.stringify(page.entries);
  const removed = id => {
    drawn.get(id)?.node.remove(); drawn.delete(id);
    if (nextOffset != null) nextOffset = Math.max(0, nextOffset - 1);
    empty.hidden = drawn.size > 0;
    firstSignature = JSON.stringify([...drawn.values()].slice(0, 24).map(v => v.entry));
  };
  const draw = (page, reset) => {
    answer = page; nextOffset = page.next;
    if (reset) {
      firstSignature = signature(page);
      const keep = new Set(page.entries.map(entry => entry.id));
      for (const [id, old] of drawn) if (!keep.has(id)) {old.node.remove(); drawn.delete(id);}
    }
    for (const entry of page.entries) {
      const sig = JSON.stringify(entry), old = drawn.get(entry.id);
      if (!reset && old?.sig === sig) continue;
      const node = old?.sig === sig ? old.node : card(entry, removed, () => [...drawn.values()].map(v => v.entry));
      if (old && node !== old.node) old.node.remove();
      drawn.set(entry.id, {sig, node, entry});
      cards.append(node);
    }
    empty.hidden = drawn.size > 0;
    sentinel.hidden = nextOffset == null;
  };
  async function refresh(offset = 0, poll = false) {
    if (!active() || offset == null || (busy && poll)) return;
    const mine = ++generation;
    current?.abort(); const stop = new AbortController(); current = stop; busy = true; polling = poll;
    const limit = setTimeout(() => stop.abort(), 10000);
    try {
      const page = await api("/api/gallery", params(offset), stop.signal);
      if (!active() || mine !== generation || !cards.isConnected) return;
      if (!poll || signature(page) !== firstSignature) draw(page, offset === 0);
      said.textContent = ""; retry.hidden = true;
    } catch (error) {
      if (active() && mine === generation && cards.isConnected) {said.textContent = error.message; retry.hidden = false;}
    } finally {
      clearTimeout(limit);
      if (mine === generation) {busy = false; current = null;}
    }
  }
  const change = values => {
    if (!active()) return;
    Object.assign(state, values); generation++; current?.abort(); clearTimeout(debounce); busy = true;
    debounce = setTimeout(() => refresh(0), 250);
  };
  const paint = () => tools.replaceChildren(
    dropdown({options:kinds, value:state.kind, onPick:kind => {change({kind}); paint();}}),
    dropdown({glyph:"sort", options:sorts, value:state.sort, onPick:sort => {change({sort}); paint();}}));
  const search = el("input", "search-input");
  Object.assign(search, {type:"search", placeholder:"Search files…", value:state.search, spellcheck:false});
  search.setAttribute("aria-label", "Search files");
  search.addEventListener("input", () => change({search:search.value.trim()}));
  paint(); draw(answer, true);
  const observer = new IntersectionObserver(entries => {
    if ((!busy || polling) && entries.some(entry => entry.isIntersecting)) refresh(nextOffset);
  }, {root:document.getElementById("main"), rootMargin:"400px"});
  observer.observe(sentinel);
  const timer = setInterval(() => {
    if (!active() || !cards.isConnected) {
      generation++; current?.abort(); clearTimeout(debounce); clearInterval(timer); observer.disconnect(); return;
    }
    if (!busy && document.visibilityState !== "hidden") refresh(0, true);
  }, 3000);
  return el("div", "screen gallery-screen", el("h1", "", "Gallery"),
    el("div", "gallery-toolbar", el("div", "gallery-search-tools", el("label", "search", icon("search"), search), tools)),
    said, cards, empty, sentinel, retry);
}
