// Records, tasks and activity share a batch stream and list components.
// Filters and task links translate server answers without deriving ledger rules.
import { el, api, project, link, icon } from "./ui.js";
import { happened } from "./ui-icons.js";
import { dropdown } from "./ui-parts.js";
import { card, message } from "./ui-components.js";
import { scrollBatches } from "./ui-stream.js";
let epoch = 0;
const sorts = [["recent","Latest recorded"],["oldest","Earliest recorded"],["title","Title A–Z"],["kind","Kind"]];
const kinds = [["","All kinds"],["TASK","Tasks"],["CLAIM","Claims"],["DECISION","Decisions"],["INSTRUMENT","Instruments"],["SOURCE","Sources"]];
export async function renderBrowser(screen, params = new URLSearchParams()) {
  const mine=++epoch, shown=project(), route=location.hash;
  const active=()=>mine===epoch && shown===project() && route===location.hash;
  const state={kind:params.get("kind")?.toUpperCase() || "",under:params.get("under") || "",search:"",status:"",sort:"recent"};
  const request=(offset,signal)=>api("/api/screen",{project:shown,screen,offset,...state},signal);
  const first=await request(0);
  const rows=card();rows.classList.add("browser-rows");
  const counts=el("div","browser-counts"),nesting=el("nav","gallery-tools"),tools=el("div","gallery-tools");
  const empty=message("No entries match these filters."), note=el("p","muted browser-note");
  let stream,pending;
  const drawn=new Set();
  stream=scrollBatches({first,request,active,draw:(page,reset)=>{
    if (reset) {rows.replaceChildren();drawn.clear();}
    for (const r of page.rows || []) {
      const key=r.id || `${r.sequence}:${r.title}`;if (drawn.has(key)) continue;drawn.add(key);
      const title=r.id ? link(r.id,r.title || r.id,"row-title") : el("span","row-title",r.title || r.kind);
      if (screen==="tasks" && r.nested>0) title.href="#/tasks?under="+encodeURIComponent(r.id);
      rows.append(el("div","row-line browser-row",title,el("span","muted",screen==="tasks" ? (r.nested == null ? "—" : `${r.nested} open nested`) : happened[r.kind] || r.kind),el("span","",r.status ?? "—"),el("span","muted",r.actor ?? "—")));
    }
    empty.textContent=page.result === "UNKNOWN" ? (page.reason || "Task unavailable at this ledger position.") : "No entries match these filters.";
    empty.hidden=drawn.size>0;rows.hidden=!drawn.size;note.textContent=page.note ?? "";
    counts.replaceChildren(...Object.entries(page.counts || {}).filter(([,n])=>n!=null).map(([name,n])=>el("span","muted",`${name}: ${n}`)));
    if (screen==="tasks") {const top=el("a","link","Top-level tasks");top.href="#/tasks";nesting.replaceChildren(top,...(page.parent ? [link(page.parent.id,page.parent.title)] : []));}
  }});
  const change=values=>{Object.assign(state,values);paint();stream.invalidate();clearTimeout(pending);pending=setTimeout(()=>stream.reset(),250);};
  const paint=()=>{
    tools.replaceChildren();
  const options=screen==="tasks" ? [["","All open statuses"],["READY","Ready"],["BLOCKED","Blocked"],["IN FLIGHT","In flight"]] : kinds;
  if (screen!=="history") tools.append(dropdown({options,value:screen==="tasks" ? state.status : state.kind,onPick:value=>change(screen==="tasks" ? {status:value} : {kind:value})}));
  tools.append(dropdown({glyph:"sort",options:screen==="history" ? sorts.slice(0,2) : sorts,value:state.sort,onPick:sort=>change({sort})}),stream.refresh);
  };
  paint();
  const search=el("input","search-input");Object.assign(search,{type:"search",placeholder:screen==="tasks" ? "Search these tasks…" : "Search the whole ledger…"});search.setAttribute("aria-label","Search "+screen);search.addEventListener("input",()=>change({search:search.value.trim()}));
  return el("div","screen browser-screen",el("h1","",screen==="tasks" ? "Tasks" : screen==="history" ? "Activity" : "Records"),el("div","gallery-toolbar",el("label","search",icon("search"),search),tools),screen==="tasks" ? nesting : null,counts,rows,empty,stream.sentinel,stream.error,note);
}
