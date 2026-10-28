// Gallery filters reset a shared infinite stream. Cards retain their nodes
// during append; file actions and inspection belong to their own modules.
import { el, api, project, icon } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { card, message } from "./ui-components.js";
import { scrollBatches } from "./ui-stream.js";
import { preview, fileURL } from "./ui-gallery-media.js";
import { galleryActions, titleButton } from "./ui-gallery-actions.js";
import { inspect } from "./ui-gallery-inspect.js";
let epoch = 0;
const state = {project:"", search:"", kind:"", sort:"recent"};
const kinds = [["","All types"],["image","Images"],["video","Videos"],["text","Text"],["other","Other"]];
const sorts = [["recent","Newest first"],["oldest","Oldest first"],["title","Title A–Z"],["kind","Kind"]];
function galleryCard(entry, removed) {
  const said = message("", "error"); said.classList.add("action-error");
  const media = preview(entry, fileURL(entry, entry.kind === "image" ? "thumb" : "file"));
  const stage = el("div", "artifact-preview", media);
  stage.tabIndex = 0; stage.setAttribute("role", "button"); stage.setAttribute("aria-label", "Open " + (entry.title || "file"));
  const open = () => inspect(entry, stage, removed);
  stage.addEventListener("click", e => {if (!e.target.closest("button,video")) open();});
  stage.addEventListener("keydown", e => {if (e.target === stage && (e.key === "Enter" || e.key === " ")) {e.preventDefault();open();}});
  const node = card(stage, el("div", "gallery-card-body", el("h2", "gallery-title", titleButton(entry, said)), galleryActions(entry, said, removed), said));
  node.classList.add("gallery-card"); node.dataset.kind = entry.kind || "other";
  return node;
}
export async function renderGallery() {
  const mine = ++epoch, shown = project(), route = location.hash;
  const active = () => mine === epoch && shown === project() && route === location.hash;
  if (state.project !== shown) Object.assign(state,{project:shown,search:"",kind:""});
  const request = (offset, signal) => api("/api/gallery", {project:shown,offset,search:state.search,kind:state.kind,sort:state.sort}, signal);
  const first = await request(0);
  const cards = el("div", "gallery-cards"), tools = el("div", "gallery-tools"), empty = message("No files match these filters.");
  const drawn = new Map();
  let stream, pending;
  const removed = id => {drawn.get(id)?.remove();drawn.delete(id);stream.removed();empty.hidden=drawn.size>0;};
  stream = scrollBatches({first,request,active,draw:(page,reset) => {
    if (reset) {cards.replaceChildren();drawn.clear();}
    for (const entry of page.entries || []) {
      if (drawn.has(entry.id)) continue;
      const node=galleryCard(entry,removed);drawn.set(entry.id,node);cards.append(node);
    }
    empty.hidden=drawn.size>0;
  }});
  const change = values => {Object.assign(state,values);paint();stream.invalidate();clearTimeout(pending);pending=setTimeout(()=>stream.reset(),250);};
  const paint = () => tools.replaceChildren(dropdown({options:kinds,value:state.kind,onPick:kind=>change({kind})}),dropdown({glyph:"sort",options:sorts,value:state.sort,onPick:sort=>change({sort})}),stream.refresh);
  paint();
  const search=el("input","search-input");Object.assign(search,{type:"search",placeholder:"Search files…",value:state.search});search.setAttribute("aria-label","Search files");
  search.addEventListener("input",()=>change({search:search.value.trim()}));
  return el("div","screen gallery-screen",el("h1","","Gallery"),el("div","gallery-toolbar",el("div","gallery-search-tools",el("label","search",icon("search"),search),tools)),cards,empty,stream.sentinel,stream.error);
}
