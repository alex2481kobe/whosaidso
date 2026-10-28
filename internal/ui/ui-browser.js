// Bounded project screens share search, paging and refresh behavior here.
// The server owns selection, totals, status and preview truncation.
import { el, api, project, link, icon } from "./ui.js";
import { happened } from "./ui-icons.js";
import { dropdown } from "./ui-parts.js";
let epoch = 0;
const states = new Map();
const sorts = [["recent", "Latest recorded"], ["oldest", "Earliest recorded"], ["title", "Title A–Z"], ["kind", "Kind"]];
const kinds = [["", "All kinds"], ["TASK", "Tasks"], ["CLAIM", "Claims"], ["DECISION", "Decisions"], ["INSTRUMENT", "Instruments"], ["SOURCE", "Sources"]];

export async function renderBrowser(screen, params = new URLSearchParams()) {
  const mine = ++epoch, shown = project(), route = location.hash;
  const active = () => mine === epoch && shown === project() && route === location.hash;
  const key = shown + ":" + screen;
  if (!states.has(key)) states.set(key, {search:"", kind:params.get("kind")?.toUpperCase() || "", status:"", sort:"recent", offset:0});
  const state = states.get(key);
 const requestedKind = params.get("kind")?.toUpperCase() || "";
 if (state.routeKind !== requestedKind) Object.assign(state, {routeKind:requestedKind, kind:requestedKind, status:"", offset:0});
  const under = screen === "tasks" ? params.get("under") || "" : "";
  if (state.under !== under) Object.assign(state, {under, offset:0, search:"", status:""});
  const request = () => api("/api/screen", {project:shown, screen, under, offset:state.offset, search:state.search, kind:state.kind, status:state.status, sort:state.sort});
  let page = await request();
  if (!active()) return el("div");
  const rows = el("section", "panel browser-rows");
  const counts = el("div", "browser-counts");
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const range = el("span", "gallery-footnote"); range.setAttribute("role", "status");
  const button = (label, fn) => { const b = el("button", "button", label); b.type = "button"; b.addEventListener("click", fn); return b; };
  const previous = button("Previous", () => refresh(page.previous));
  const next = button("Next", () => refresh(page.next));
  const note = el("p", "muted browser-note");
  const tools = el("div", "gallery-tools");
  const nesting = el("nav", "gallery-tools");
  let generation = 0, busy = false, pending;

  function draw(answer) {
    if (JSON.stringify(answer.watermark) === JSON.stringify(page.watermark)) answer.counts = {...page.counts, ...answer.counts};
    page = answer; state.offset = page.offset;
    const titleLink = r => {
      const a = link(r.id, r.title || r.id, "row-title");
      if (screen === "tasks" && r.nested > 0) a.href = "#/tasks?under=" + encodeURIComponent(r.id);
      return a;
    };
    if (screen === "tasks") {
      const top = el("a", "link", "Top-level tasks"); top.href = "#/tasks";
      nesting.replaceChildren(top, ...(page.parent ? [link(page.parent.id, page.parent.title)] : []));
    }
    const nodes = page.rows.map(r => el("div", "row-line browser-row", r.id ? titleLink(r) : el("span", "row-title", r.title || r.kind), el("span", "muted", screen === "tasks" ? `${r.nested} open nested` : happened[r.kind] || r.kind), el("span", "", r.status), el("span", "muted", r.actor)));
    rows.replaceChildren(...(nodes.length ? nodes : [el("p", "empty", "No entries match these filters.")]));
    previous.disabled = page.previous == null; next.disabled = page.next == null;
    range.textContent = page.total ? `${page.offset + 1}–${page.offset + page.rows.length} of ${page.total}` : "0 entries";
    note.textContent = page.note;
    counts.replaceChildren(...Object.entries(page.counts).map(([label, n]) => el("span", "muted", `${label}: ${n}`)));
  }
  async function refresh(offset = state.offset) {
    if (!active() || offset == null) return;
    state.offset = offset;
    const version = ++generation; busy = true;
    try {
      const answer = await request();
      if (!active() || version !== generation || !rows.isConnected) return;
      if (JSON.stringify(answer) !== JSON.stringify(page)) draw(answer);
      said.textContent = "";
    } catch (error) { if (active() && version === generation) said.textContent = "Could not refresh: " + error.message; }
    finally { if (version === generation) busy = false; }
  }
  const paintTools = () => tools.replaceChildren(
    screen === "tasks" ? dropdown({options:[["","All open statuses"],["READY","Ready"],["BLOCKED","Blocked"],["IN FLIGHT","In flight"]], value:state.status, onPick:status => change({status})}) : screen !== "history" ? dropdown({options:kinds, value:state.kind, onPick:kind => change({kind, status:""})}) : null,
    dropdown({glyph:"sort", options:screen === "history" ? sorts.slice(0,2) : sorts, value:state.sort, onPick:sort => change({sort})}),
    state.status ? button("Clear status: " + state.status, () => change({status:""})) : null);
  function change(values) {
    if (!active()) return;
    Object.assign(state, values, {offset:0}); generation++; clearTimeout(pending); busy = true;
    paintTools(); pending = setTimeout(() => refresh(0), 250);
  }
  const search = el("input", "search-input");
  Object.assign(search, {type:"search", placeholder:screen === "tasks" ? "Search these tasks…" : "Search the whole ledger…", value:state.search, spellcheck:false});
  search.setAttribute("aria-label", "Search " + screen);
  search.addEventListener("input", () => change({search:search.value.trim()}));
  draw(page); paintTools();
  const timer = setInterval(() => {
    if (!active() || !rows.isConnected) { clearInterval(timer); clearTimeout(pending); return; }
    if (!busy && document.visibilityState !== "hidden") refresh();
  }, 3000);
  return el("div", "screen browser-screen", el("h1", "", (screen === "records" ? requestedKind === "TASK" ? "Tasks" : "Records" : {home:"Overview", tasks:"Tasks", history:"Activity"}[screen])),
    el("div", "gallery-toolbar", el("label", "search", icon("search"), search), tools),
    screen === "tasks" ? nesting : null, counts, said, rows, el("nav", "gallery-tools", previous, range, next), note);
}
