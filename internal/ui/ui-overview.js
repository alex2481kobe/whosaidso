// Overview displays the server's two owner-action queues with the same
// cards, empty states and batch stream as the other screens.
import { el, api, project, link } from "./ui.js";
import { countsLine, fillCounts, listCard, listRow, message } from "./ui-components.js";
import { happened } from "./ui-icons.js";
import { scrollBatches } from "./ui-stream.js";
export async function renderOverview() {
  const shown=project(),route=location.hash;
  const active=()=>shown===project() && route===location.hash;
  const request=(offset,signal)=>api("/api/screen",{project:shown,screen:"home",offset},signal);
  const first=await request(0);
  const counts=countsLine();
  const panels={in_flight:listCard("In flight"),awaiting_decision:listCard("Awaiting your decision")};
  const empty={in_flight:message("None awaiting action."),awaiting_decision:message("None awaiting action.")};
  for (const group of Object.keys(panels)) panels[group].append(empty[group]);
  const drawn=new Set();
  const stream=scrollBatches({first,request,active,draw:(page,reset)=>{
    if (reset) {for (const group of Object.keys(panels)) {for (const row of panels[group].querySelectorAll(".row-line")) row.remove();empty[group].hidden=false;}drawn.clear();}
    for (const row of page.rows || []) {
      if (!panels[row.group] || drawn.has(row.id)) continue;drawn.add(row.id);empty[row.group].hidden=true;
      panels[row.group].append(listRow(link(row.id,row.title,"row-title"),el("span","muted",happened[row.kind] || row.kind),el("span","",row.status ?? "—"),el("span","muted",row.actor ?? "—")));
    }
    fillCounts(counts,page.counts);
  }});
  return el("div","screen browser-screen overview-screen",el("h1","","Overview"),el("div","counts-row",counts,stream.refresh),panels.in_flight,panels.awaiting_decision,stream.sentinel,stream.error);
}
