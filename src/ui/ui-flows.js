import { el, api } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { attention, diagram } from "./ui-flow-diagram.js";

const route = (id = "", parent = "", branch = "", offset = 0) => "#/flows?" + new URLSearchParams({id,parent,branch,offset});
export function flowsScreen() {
  const query = new URLSearchParams(location.hash.split("?")[1] || "");
  const id = query.get("id") || "", parent = query.get("parent") || "", branch = query.get("branch") || "", offset = Number(query.get("offset") || 0);
  const toolbar = el("div", "toolbar flow-toolbar"), crumbs = el("nav", "flow-crumbs"), choices = el("div", "flow-choices"), body = el("div", "flow-canvas"), pages = el("div", "toolbar"), panel = el("aside", "flow-panel card");
  crumbs.setAttribute("aria-label","Breadcrumb"); panel.hidden = true; panel.setAttribute("aria-label","Details");
  const node = el("div", "screen flow-screen", toolbar,body,pages,panel); toolbar.append(crumbs,choices);
  let layer = parent; const collapsed = new Set();
  let data, returnFocus, selected = null, navKey = "", detailKey = "", cardsKey = "";
  const link = (name, target = id, layer = "", page = 0) => Object.assign(el("a", "flow-link", name), {href:route(target,layer,branch,page)});
  const go = (n, href = false) => {
    const path = route(id,n.target || n.id,branch);
    if (href) return path;
    location.hash = path;
  };
  const chart = diagram(go,n => show(n.id),pick => {
    selected = pick; if (!panel.hidden) details(); chart.update(data,selected);
  });
  if (id) body.append(chart.node);
  const close = () => {
    const old = selected; selected = null; panel.hidden = true; chart.update(data);
    if (returnFocus?.isConnected) returnFocus.focus();
    else [...body.querySelectorAll("[data-node]")].find(n => n.dataset.node === old)?.querySelector("[role=button]")?.focus();
  };
  const heading = el("h2"), fields = el("div", "flow-fields"), closeButton = el("button", "flow-link", "Close");
  closeButton.type = "button"; closeButton.onclick = close;
  panel.append(el("div", "flow-panel-head",heading,closeButton),fields);
  node.addEventListener("keydown",e => {if (e.key === "Escape" && !panel.hidden) close();});
  function show(pick, focus = true) {
    if (focus) returnFocus = document.activeElement;
    selected = pick; panel.hidden = false; details(); chart.update(data,selected);
    if (focus) closeButton.focus();
  }
  function details() {
    if (selected == null || !data) return;
    const n = selected === "$screen" ? data : data.nodes?.find(n => n.id === selected);
    const next = JSON.stringify([n,data.steps,data.unplanned,data.unknown,data.risks,data.freshness]);
    if (next === detailKey) return; detailKey = next;
    heading.textContent = n?.name || (selected === "$screen" ? "Details" : "Removed");
    const rows = [];
    const field = (label,value) => {if (value != null && value !== "" && (!Array.isArray(value) || value.length)) rows.push(el("div", "flow-field",el("span","muted",label),el("div","",Array.isArray(value) ? value.join(", ") : value)));};
    field("State",n?.state); field("Path",n?.path); field("Line",n?.line); field("Paths",n?.paths); field("Functions",n?.symbols);
    field("Evidence",n?.evidence); field("Freshness",data.freshness); field("Note",n?.note); field("References",n?.refs);
    if (n?.goal) rows.push(el("div","flow-field",el("span","muted","Goal"),Object.assign(el("a","flow-link",n.goal),{href:"#/goals?goal="+encodeURIComponent(n.goal)})));
    const risks = selected === "$screen" ? (data.risks || []).map(r => r.risk) : n?.risks || [];
    for (const r of risks) {field("Risk",r.note); field("Mitigation",Object.entries(r.mitigation).map(([k,v]) => `${k}: ${v}`));}
    for (const s of data.steps || []) if (s.from === selected || s.to === selected) {field("When",s.when); field("Arrow",`${s.state} · ${s.evidence || ""}`);}
    for (const u of data.unplanned || []) field("Not in plan",[u.path,u.to].filter(Boolean).join(" → "));
    field("Not checked",data.unknown); field("Problems",data.problems); field("Code limits",data.gaps);
    if (!n) field("Box","This box was removed.");
    fields.replaceChildren(...rows);
  }
  function card(title, target, progress) {
    const a = link(title,target); a.className = "flow-card card";
    const preview = document.createElementNS("http://www.w3.org/2000/svg","svg"); preview.setAttribute("viewBox","0 0 180 42"); preview.setAttribute("aria-hidden","true");
    for (let i=0;i<3;i++) {const r=document.createElementNS(preview.namespaceURI,"rect"); for (const [k,v] of Object.entries({x:i*66+1,y:5,width:44,height:30,rx:8})) r.setAttribute(k,v); preview.append(r); if(i<2){const p=document.createElementNS(preview.namespaceURI,"path");p.setAttribute("d",`M ${i*66+45} 20 h 20 m -5 -4 l 5 4 -5 4`);preview.append(p);}}
    a.replaceChildren(preview,el("strong","",title),...(progress ? [el("span","muted",`${progress.built} of ${progress.total} built`)] : [])); return a;
  }
  return {node, dispose:chart.disconnect, async load() {
    data = await api("/api/flows",{id,parent:layer,offset,branch});
    const visited = new Set([layer]);
    for (;;) {
      const only = data.nodes?.length === 1 ? data.nodes[0] : null;
      if (!id || offset || data.next != null || !only?.children || only.note || only.goal || only.refs?.length || only.risks?.length || ["steps","risks","unknown","unplanned","problems"].some(k => data[k]?.length)) break;
      const next = only.target || only.id;
      if (visited.has(next)) break;
      const child = await api("/api/flows",{id,parent:next,offset:0,branch});
      if (!child.nodes?.length) break;
      collapsed.add(next); visited.add(next); layer = next; data = child;
    }
    node.dataset.revision = data.revision || "";
    const nav = JSON.stringify([data.breadcrumbs,data.branches,data.committed_only,data.next]);
    if (nav !== navKey) {
      navKey = nav;
      const trail = [link("Flows", "")];
      if (id) trail.push(link(id === "$map" ? "Code map" : id.replace(/[-_]/g," ").replace(/^./,c => c.toUpperCase())));
      for (const c of (data.breadcrumbs || []).filter(c => c.id === layer || !collapsed.has(c.id))) trail.push(link(c.name,id,c.id));
      crumbs.replaceChildren(...trail.flatMap((a,i) => i ? [el("span","muted",">"),a] : [a]));
      const details = el("button","flow-link","Details"); details.type="button"; details.onclick=() => show(selected || "$screen");
      choices.replaceChildren(...[id && details,data.branches?.length && dropdown({options:[["","Worktree"],...data.branches.map(b => [b,b])],value:branch,onPick:b => {location.hash=route(id,"",b);}}),data.committed_only && el("span","muted","committed only")].filter(Boolean));
      pages.replaceChildren(...[offset > 0 && link("Previous",id,layer,Math.max(0,offset-20)),data.next != null && link("Next",id,layer,data.next)].filter(Boolean));
    }
    if (!id) {
      const key=JSON.stringify(data.flows);
      if (key !== cardsKey) {cardsKey=key; body.className="flow-grid"; body.replaceChildren(card("Code map","$map"),...(data.flows || []).map(f => card(f.id.replace(/[-_]/g," ").replace(/^./,c => c.toUpperCase()),f.id,f.progress)));}
    } else {
      chart.update(data,selected);
      body.dataset.empty = data.nodes?.length ? "" : data.problems?.length ? "Plan needs attention" : attention[data.state] || "No calls here";
    }
    details(); return data.version;
  }};
}
