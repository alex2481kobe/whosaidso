const NS = "http://www.w3.org/2000/svg";
const svg = (tag, attrs = {}, text) => {
  const n = document.createElementNS(NS, tag);
  for (const [key, value] of Object.entries(attrs)) n.setAttribute(key, value);
  if (text != null) n.textContent = text;
  return n;
};
export const attention = {planned:"not built yet", authored:"not checked", drifted:"changed", missing:"missing", unplanned:"not in plan", unknown:"not checked", ambiguous:"not checked", broken:"not checked"};
function wrap(text, width, size = 18) {
  const canvas = document.createElement("canvas"), ctx = canvas.getContext("2d");
  ctx.font = `550 ${size}px system-ui`;
  const lines = []; let line = "";
  for (const word of String(text).split(/\s+/)) {
    if (line && ctx.measureText(line + " " + word).width > width) { lines.push(line); line = ""; }
    for (const char of (line ? " " : "") + word) {
      if (ctx.measureText(line + char).width > width) { lines.push(line); line = ""; }
      line += char;
    }
  }
  if (line) lines.push(line);
  return lines;
}
// Remove only DFS back-edges from ranking; they remain arrows in the picture.
function layout(nodes, edges, width) {
  const ids = new Set(nodes.map(n => n.id)), marks = new Map(), order = [], forward = [];
  const visit = id => {
    marks.set(id, 1);
    for (const e of edges.filter(e => e.from === id && ids.has(e.to))) {
      if (marks.get(e.to) === 1) continue;
      forward.push(e); if (!marks.has(e.to)) visit(e.to);
    }
    marks.set(id, 2); order.unshift(id);
  };
  nodes.forEach(n => { if (!marks.has(n.id)) visit(n.id); });
  const rank = new Map(nodes.map(n => [n.id, 0]));
  for (const id of order) for (const e of forward.filter(e => e.from === id)) rank.set(e.to, Math.max(rank.get(e.to), rank.get(id) + 1));
  const columns = Math.max(1, Math.floor((width - 72) / 260)), boxWidth = Math.min(280, (width - 72 - (columns - 1) * 40) / columns);
  const returns = new Map();
  for (const e of edges) if (rank.has(e.to) && rank.get(e.to) <= rank.get(e.from)) returns.set(e.to,(returns.get(e.to) || 0)+1);
  const lanes = Math.max(0,...returns.values()), gap = Math.max(92,44+lanes*18);
  const points = new Map(); let y = 72+lanes*18, row = 0;
  for (const level of [...new Set(rank.values())].sort((a,b) => a-b)) {
    const group = nodes.filter(n => rank.get(n.id) === level);
    for (let i = 0; i < group.length; i += columns) {
      const chunk = group.slice(i, i + columns), heights = chunk.map(n => Math.max(92, wrap(n.name, boxWidth - 44).length * 24 + 60));
      const height = Math.max(...heights), left = (width - chunk.length * boxWidth - (chunk.length - 1) * 40) / 2;
      chunk.forEach((n,j) => points.set(n.id, {x:left + j * (boxWidth + 40), y, w:boxWidth, h:heights[j], floor:y+height, row}));
      y += height + gap; row++;
    }
  }
  return {points, height:Math.max(180, y + 24)};
}
export function diagram(onGo, onDetails) {
  const root = svg("svg", {class:"flow-diagram", role:"group", "aria-label":"Flowchart"});
  const defs = svg("defs"), marker = svg("marker", {id:"flow-arrow", viewBox:"0 0 10 10", refX:9, refY:5, markerWidth:7, markerHeight:7, orient:"auto-start-reverse"});
  marker.append(svg("path", {d:"M 1 1 L 9 5 L 1 9", fill:"none", stroke:"currentColor", "stroke-width":1.5})); defs.append(marker);
  const lines = svg("g", {class:"flow-arrows"}), boxes = svg("g", {class:"flow-boxes"}); root.append(defs, lines, boxes);
  const items = new Map(), arrows = new Map(); let latest, selected = "", shape = "", points;
  const redraw = () => {
    if (!latest) return;
    const width = Math.max(220, root.parentElement?.clientWidth || 960);
    const nodes = latest.nodes || [], edges = (latest.steps || []).flatMap((s,i) => (Array.isArray(s.to) ? s.to : [s.to]).map((to,j) => ({...s,to,key:`${s.id || i}:${j}`})));
    const signature = JSON.stringify([width,nodes.map(n => [n.id,n.name]),edges.map(e => [e.key,e.from,e.to,e.when])]);
    const moved = signature !== shape;
    if (moved) { const plan = layout(nodes,edges,width); points = plan.points; root.setAttribute("viewBox", `0 0 ${width} ${plan.height}`); root.style.height = `${plan.height}px`; shape = signature; }
    const keep = new Set();
    for (const n of nodes) {
      keep.add(n.id); let item = items.get(n.id);
      if (!item) {
        const g = svg("g", {class:"flow-box", "data-node":n.id}), a = svg("a"), rect = svg("rect", {rx:16}), title = svg("text", {class:"flow-name"}), state = svg("text", {class:"flow-word"}), deeper = svg("text", {class:"flow-deeper"}, "›");
        a.append(rect,title,state,deeper);
        const info = svg("g", {class:"flow-info", role:"button", tabindex:0}), hit = svg("rect", {width:62,height:26,rx:7}), label = svg("text", {x:31,y:18,"text-anchor":"middle"}, "Details"); info.append(hit,label); g.append(a,info); boxes.append(g);
        item = {g,a,rect,title,state,deeper,info}; items.set(n.id,item);
        a.addEventListener("click", e => {e.preventDefault(); onGo(item.data);});
        const detail = e => { e.preventDefault(); e.stopPropagation(); onDetails(item.data); };
        info.addEventListener("click",detail); info.addEventListener("keydown",e => {if (["Enter"," "].includes(e.key)) detail(e);});
      }
      item.data = n; const p = points.get(n.id);
      item.g.setAttribute("transform",`translate(${p.x} ${p.y})`); item.g.dataset.state = n.state;
      item.g.classList.toggle("selected",n.id === selected);
      item.a.setAttribute("href",onGo(n,true)); item.a.setAttribute("aria-label",`Open ${n.name}`);
      item.rect.setAttribute("width",p.w); item.rect.setAttribute("height",p.h);
      if (moved) { const rows = wrap(n.name,p.w-44); item.title.replaceChildren(...rows.map((line,i) => svg("tspan",{x:22,y:32+i*24},line))); }
      item.state.setAttribute("x",22); item.state.setAttribute("y",p.h-19); item.state.textContent = attention[n.state] || "";
      item.deeper.setAttribute("x",p.w-23); item.deeper.setAttribute("y",32); item.deeper.style.display = n.children ? "" : "none";
      item.info.setAttribute("transform",`translate(${p.w-70} ${p.h+5})`); item.info.setAttribute("aria-label",`Details for ${n.name}`);
    }
    for (const [id,item] of items) if (!keep.has(id)) {item.g.remove(); items.delete(id);}
    const used = new Set();
    const returnSlots = new Map();
    edges.forEach((e,i) => {
      const a = points.get(e.from), b = points.get(e.to); if (!a || !b) return;
      used.add(e.key); let item = arrows.get(e.key);
      if (!item) {const g = svg("g"), path = svg("path", {"marker-end":"url(#flow-arrow)"}), label = svg("text", {"text-anchor":"middle"}); g.append(path,label); lines.append(g); item={g,path,label}; arrows.set(e.key,item);}
      item.g.dataset.state = e.state;
      if (!moved) return;
      const slot = returnSlots.get(e.to) || 0; if (b.row <= a.row) returnSlots.set(e.to,slot+1);
      const ax=a.x+a.w/2, ay=a.y+a.h, bx=b.x+b.w/2, by=b.y, lane=14+(b.row<=a.row ? slot : i%3)*18;
      let d, lx, ly;
      if (b.row === a.row && a !== b) {
        const y=Math.max(a.floor,b.floor)+22+lane; d=`M ${ax} ${ay} V ${y} H ${bx} V ${b.y+b.h}`; lx=(ax+bx)/2; ly=y-7;
      } else if (b.row > a.row && b.row === a.row+1) {
        const mid=(ay+by)/2; d=`M ${ax} ${ay} V ${mid} H ${bx} V ${by}`; lx=(ax+bx)/2; ly=mid-7;
      } else {
        const x=b.row<=a.row ? Math.min(lane,width*.12) : width-Math.min(lane,width*.12);
        d=`M ${ax} ${ay} V ${a.floor+12+lane} H ${x} V ${b.y-24-lane} H ${bx} V ${by}`; lx=bx; ly=b.y-32-lane;
      }
      item.path.setAttribute("d",d); item.label.setAttribute("x",lx); item.label.setAttribute("y",ly);
      item.label.replaceChildren(...wrap(e.when || "",Math.max(100,Math.min(220,width-80)),12).map((line,j) => svg("tspan",{x:lx,dy:j ? 15 : 0},line)));
    });
    for (const [key,item] of arrows) if (!used.has(key)) {item.g.remove(); arrows.delete(key);}
  };
  const observer = new ResizeObserver(redraw);
  return {node:root, update(data, pick = "") {latest=data; selected=pick; redraw(); if (root.isConnected) observer.observe(root.parentElement);}, disconnect() {observer.disconnect();}};
}
