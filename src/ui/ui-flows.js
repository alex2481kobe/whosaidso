// The Flows screen: one canvas. Boxes open in place, from whole features down to functions.
import { el, api } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { attention, boxSize, diagram } from "./ui-flow-diagram.js";
import { layout } from "./ui-flow-layout.js";

const COMFORT = .75, PAGES = 10;

export function flowsScreen() {
  const query = new URLSearchParams(location.hash.split("?")[1] || "");
  const canvas = el("div", "flow-canvas"), panel = el("aside", "flow-panel card"), node = el("div", "screen flow-screen", canvas, panel);
  const heading = el("h2"), fields = el("div", "flow-fields"), closeButton = el("button", "flow-link", "Close");
  closeButton.type = "button"; closeButton.onclick = () => { panel.hidden = true; };
  panel.append(el("div", "flow-panel-head", heading, closeButton), fields); panel.hidden = true; panel.setAttribute("aria-label", "Details");
  node.addEventListener("keydown", e => { if (e.key === "Escape") panel.hidden = true; });

  const nodes = new Map(), kids = new Map(), steps = new Map(), open = new Set(), loaded = new Set();
  let id = query.get("id") || "", started = false, picked = "", drawn = [];
  const add = (n, parent) => {
    if (nodes.has(n.id)) { nodes.set(n.id, {...nodes.get(n.id), ...n, parent: nodes.get(n.id).parent}); return; }
    nodes.set(n.id, {...n, parent});
    if (!kids.has(parent)) kids.set(parent, []);
    kids.get(parent).push(n.id);
  };
  const known = end => nodes.has(end) ? end : end?.startsWith("outside:") && nodes.has(end.slice(8)) ? end.slice(8) : "";
  const link = s => {
    const from = known(s.from), to = known(s.to);
    if (from && to) steps.set(s.id ? `${s.id} ${from} ${to}` : `${from} ${to}`, {when: s.when, state: s.state, from, to});
  };
  // Boxes first, then arrows, because an arrow may lead to a box further down the same page.
  const merge = (parent, answer) => {
    for (const n of answer.nodes || []) if (!n.id.startsWith("outside:")) add(n, parent);
    for (const s of answer.steps || []) link(s);
  };
  async function fetchLayer(parent) {
    let offset = 0;
    for (let page = 0; page < PAGES; page++) {
      const answer = await api("/api/flows", {id, parent, offset});
      merge(parent, answer);
      if (answer.next == null) break;
      offset = answer.next;
    }
    loaded.add(parent);
  }
  const under = (end, parent) => { let at = end; while (at && nodes.get(at)?.parent !== parent) at = nodes.get(at)?.parent; return at; };
  const isOpen = n => open.has(n) && (kids.get(n)?.length > 0);
  // An arrow into a closed box ends at that box, so abstract levels stay connected.
  const seen = end => {
    const chain = [];
    for (let at = end; at; at = nodes.get(at)?.parent) chain.unshift(at);
    return chain.find(n => !isOpen(n)) || end;
  };
  const inside = (a, b) => { for (let at = a; at; at = nodes.get(at)?.parent) if (at === b) return true; return false; };
  const edges = () => {
    const pairs = new Map();
    for (const s of steps.values()) {
      const from = seen(s.from), to = seen(s.to);
      if (from === to || inside(from, to) || inside(to, from)) continue;
      // Arrows both ways between two boxes are one line with two heads.
      const way = {from, to, when: s.when}, old = pairs.get(`${from} ${to}`) || pairs.get(`${to} ${from}`);
      if (!old) { const key = `${from} ${to}`; pairs.set(key, {key, from, to, when: s.when, state: s.state, ways: [way]}); continue; }
      old.ways.push(way); old.both ||= old.from === to;
      if (attention[s.state]) old.state = s.state;
    }
    return [...pairs.values()];
  };
  const graph = {
    children: parent => kids.get(parent) || [],
    isOpen,
    size: n => { const {w, h} = boxSize(nodes.get(n)); return isOpen(n) ? {w: Math.max(w + 60, 150), h} : {w, h}; },
    links(parent, ids) {
      const set = new Set(ids), pairs = new Map();
      for (const e of drawn) {
        const a = under(e.from, parent), b = under(e.to, parent);
        if (!a || !b || a === b || !set.has(a) || !set.has(b)) continue;
        pairs.set(`${a} ${b}`, [a, b]);
        if (e.both) pairs.set(`${b} ${a}`, [b, a]);
      }
      return [...pairs.values()];
    },
  };
  const arranged = () => { drawn = edges(); return layout(graph); };
  const draw = keep => {
    const rects = arranged();
    canvas.dataset.empty = nodes.size ? "" : "Nothing here yet";
    chart.update({nodes, rects, edges: drawn, origin: n => rects.get(nodes.get(n)?.parent)}, keep);
  };
  async function toggle(n) {
    const item = nodes.get(n);
    if (!isOpen(n) && !item.children) return details(n);
    if (open.delete(n)) return draw(n);
    if (!kids.get(n)?.length) await fetchLayer(n);
    if (kids.get(n)?.length) { open.add(n); draw(n); } else details(n);
  }
  function details(n) {
    const item = nodes.get(n);
    heading.textContent = item.name;
    const rows = [], field = (label, value) => { if (value != null && value !== "" && (!Array.isArray(value) || value.length)) rows.push(el("div", "flow-field", el("span", "muted", label), el("div", "", Array.isArray(value) ? value.join(", ") : value))); };
    field("State", attention[item.state]); field("File", item.path); field("Line", item.line); field("Files", item.paths); field("Functions", item.symbols);
    field("Note", item.note); field("References", item.refs);
    if (item.goal) rows.push(el("div", "flow-field", el("span", "muted", "Goal"), Object.assign(el("a", "flow-link", item.goal), {href: "#/goals?goal=" + encodeURIComponent(item.goal)})));
    for (const r of item.risks || []) { field("Risk", r.note); field("Mitigation", Object.entries(r.mitigation).map(([k, v]) => `${k}: ${v}`)); }
    for (const e of edges().flatMap(e => e.ways)) if (e.from === n || e.to === n) field(e.from === n ? `To ${nodes.get(e.to).name}` : `From ${nodes.get(e.from).name}`, e.when || "called");
    fields.replaceChildren(...rows); panel.hidden = false; closeButton.focus();
  }
  const chart = diagram({onToggle: toggle, onDetails: details});
  const button = (text, label, act) => { const b = el("button", "flow-tool", text); b.type = "button"; b.setAttribute("aria-label", label); b.onclick = act; return b; };
  const tools = el("div", "flow-tools", button("+", "Zoom in", () => chart.zoom(1.25)), button("−", "Zoom out", () => chart.zoom(.8)), button("Fit", "Fit to screen", () => chart.fit()));
  const plans = el("div", "flow-plans");
  canvas.append(chart.node, plans, tools);

  return {node, dispose: chart.disconnect, async load() {
    const list = await api("/api/flows"), names = (list.flows || []).map(f => f.id);
    if (!names.includes(id)) id = names[0] || "$map";
    if (id !== picked) {
      picked = id;
      plans.replaceChildren(...(names.length > 1 ? [dropdown({options: names.map(f => [f, f.replace(/[-_]/g, " ").replace(/^./, c => c.toUpperCase())]), value: id, onPick: f => { location.hash = "#/flows?id=" + encodeURIComponent(f); }})] : []));
    }
    const data = await api("/api/flows", {id}), tree = data.tree;
    if (data.problems?.length) { canvas.dataset.empty = "Plan needs attention"; return data.version; }
    nodes.clear(); kids.clear(); steps.clear();
    if (tree) {
      for (const n of tree.nodes) add(n, n.parent || "");
      for (const s of tree.steps) link(s);
    } else merge("", data);
    if (!tree) for (let at = data.next; at != null;) { const more = await api("/api/flows", {id, offset: at}); merge("", more); at = more.next; }
    for (const parent of loaded) await fetchLayer(parent);
    if (!started) {
      started = true;
      // Open the whole plan when it still reads at a comfortable size, otherwise start at the big boxes.
      for (const n of nodes.keys()) if (kids.get(n)?.length) open.add(n);
      const all = [...arranged().values()], wide = Math.max(0, ...all.map(r => r.x + r.w)), tall = Math.max(0, ...all.map(r => r.y + r.h));
      if (wide > (chart.node.clientWidth || innerWidth - 240) / COMFORT || tall > (innerHeight - 120) / COMFORT) open.clear();
    }
    draw(); return data.version;
  }};
}
