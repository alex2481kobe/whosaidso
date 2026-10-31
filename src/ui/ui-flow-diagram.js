// The flowchart canvas: drag to move, pinch or Ctrl-scroll to zoom, boxes glide when one opens.
import { routes } from "./ui-flow-layout.js";

const NS = "http://www.w3.org/2000/svg";
const svg = (tag, attrs = {}, text) => {
  const n = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
  if (text != null) n.textContent = text;
  return n;
};
export const attention = {planned: "not built yet", drifted: "changed", missing: "missing", unplanned: "not in plan"};
const ctx = document.createElement("canvas").getContext("2d");
const measure = (text, size = 14) => { ctx.font = `550 ${size}px system-ui`; return ctx.measureText(text).width; };
export function wrap(text, width, size = 14) {
  const rows = []; let row = "";
  for (const word of String(text).split(/\s+/)) {
    if (row && measure(row + " " + word, size) > width) { rows.push(row); row = ""; }
    for (const c of (row ? " " : "") + word) { if (measure(row + c, size) > width) { rows.push(row); row = ""; } row += c; }
  }
  if (row) rows.push(row);
  return rows;
}
export const boxSize = n => {
  const w = Math.max(116, Math.min(216, measure(n.name) + 64)), rows = wrap(n.name, w - 64);
  return {w, h: 50 + (rows.length - 1) * 18 + (attention[n.state] ? 16 : 0), rows};
};
const MOVE = 320, READABLE = .6, ease = t => 1 - Math.pow(1 - t, 3);
const still = matchMedia("(prefers-reduced-motion: reduce)");
const between = (a, b, t) => Object.fromEntries(["x", "y", "w", "h"].map(k => [k, a[k] + (b[k] - a[k]) * t]));

export function diagram({onToggle, onDetails}) {
  const root = svg("svg", {class: "flow-diagram", role: "group", "aria-label": "Flowchart"});
  const defs = svg("defs"), world = svg("g"), frames = svg("g"), lines = svg("g", {class: "flow-arrows"}), labels = svg("g"), boxes = svg("g");
  for (const [id, cls] of [["flow-arrow", "flow-head"], ["flow-arrow-active", "flow-head active"]]) {
    const m = svg("marker", {id, viewBox: "0 0 10 10", refX: 9, refY: 5, markerWidth: 7, markerHeight: 7, orient: "auto-start-reverse"});
    m.append(svg("path", {class: cls, d: "M 1.5 1.5 L 8.5 5 L 1.5 8.5"})); defs.append(m);
  }
  world.append(frames, lines, labels, boxes); root.append(defs, world);
  const items = new Map(), arrows = new Map();
  let model = {nodes: new Map(), rects: new Map(), edges: []}, shown = new Map(), view = {x: 0, y: 0, k: 1}, focus = "", frame = 0, fitted = false, shape = "";

  const apply = () => {
    world.setAttribute("transform", `translate(${view.x} ${view.y}) scale(${view.k})`);
    const dot = 22 * view.k;
    root.style.backgroundSize = `${dot}px ${dot}px`;
    root.style.backgroundPosition = `${view.x}px ${view.y}px`;
  };
  const size = () => ({w: root.clientWidth || 960, h: root.clientHeight || 600});
  const bounds = rects => {
    const all = [...rects.values()];
    if (!all.length) return {x: 0, y: 0, w: 1, h: 1};
    const x = Math.min(...all.map(r => r.x)), y = Math.min(...all.map(r => r.y));
    return {x, y, w: Math.max(...all.map(r => r.x + r.w)) - x, h: Math.max(...all.map(r => r.y + r.h)) - y};
  };
  const fitting = (box, largest = 1.25) => {
    const {w, h} = size(), k = Math.max(.15, Math.min(largest, (w - 96) / box.w, (h - 96) / box.h));
    return {k, x: (w - box.w * k) / 2 - box.x * k, y: (h - box.h * k) / 2 - box.y * k};
  };
  // Motion shares one clock, so boxes, arrows and the camera arrive together.
  const glide = (fromRects, target, toRects) => {
    cancelAnimationFrame(frame);
    const start = performance.now(), camera = {...view}, labelsOff = labels.style;
    labelsOff.opacity = 0;
    const tick = now => {
      const t = still.matches ? 1 : ease(Math.max(0, Math.min(1, (now - start) / MOVE)));
      const rects = new Map([...toRects].map(([id, r]) => [id, {...r, ...between(fromRects.get(id) || r, r, t)}]));
      view = {x: camera.x + (target.x - camera.x) * t, y: camera.y + (target.y - camera.y) * t, k: camera.k + (target.k - camera.k) * t};
      place(rects, Math.min(1, t * 1.6)); apply();
      if (t < 1) frame = requestAnimationFrame(tick);
      else { shown = toRects; name(toRects); labelsOff.opacity = 1; }
    };
    tick(start);
  };
  const place = (rects, fade) => {
    for (const [id, item] of items) {
      const r = rects.get(id); if (!r) continue;
      item.g.setAttribute("transform", `translate(${r.x} ${r.y})`);
      item.rect.setAttribute("width", r.w); item.rect.setAttribute("height", r.h);
      if (item.fresh) item.g.style.opacity = fade;
      item.layout(r);
    }
    for (const a of routes(model.edges, rects)) arrows.get(a.key)?.path.setAttribute("d", a.d);
  };
  // A label shows only where it covers no box and no other label; the rest appear on hover.
  const name = rects => {
    const taken = [...rects.values()].filter(r => !r.open), keep = new Set();
    for (const a of routes(model.edges, rects)) {
      const item = arrows.get(a.key); if (!item) continue;
      keep.add(a.key);
      const w = measure(a.when || "", 11) + 12, h = 20, x = a.mid[0] - w / 2, y = a.mid[1] - h / 2;
      const clear = a.when && !taken.some(r => x < r.x + r.w + 4 && x + w > r.x - 4 && y < r.y + r.h + 4 && y + h > r.y - 4);
      item.label.setAttribute("transform", `translate(${x} ${y})`);
      item.tag.setAttribute("width", w); item.tag.setAttribute("height", h);
      item.word.setAttribute("x", w / 2); item.word.setAttribute("y", 14); item.word.textContent = a.when || "";
      item.label.dataset.fits = clear ? "yes" : "";
      item.label.dataset.has = a.when ? "yes" : "";
      if (clear) taken.push({x, y, w, h});
    }
    for (const [key, item] of arrows) if (!keep.has(key)) { item.path.remove(); item.label.remove(); arrows.delete(key); }
  };
  const highlight = () => {
    const near = new Set([focus]);
    for (const e of model.edges) if (e.from === focus || e.to === focus) { near.add(e.from); near.add(e.to); }
    // A box with no arrows has nothing to point out, so nothing else fades.
    const pointing = near.size > 1, within = id => { for (let at = id; at; at = model.nodes.get(at)?.parent) if (near.has(at)) return true; return false; };
    for (const [id, item] of items) item.g.classList.toggle("dimmed", pointing && !within(id) && !model.rects.get(id)?.open);
    for (const item of arrows.values()) {
      const on = !!focus && (item.from === focus || item.to === focus);
      item.path.classList.toggle("active", on); item.label.classList.toggle("active", on);
      item.path.classList.toggle("dimmed", pointing && !on);
      const head = on ? "url(#flow-arrow-active)" : "url(#flow-arrow)";
      item.path.setAttribute("marker-end", head);
      if (item.both) item.path.setAttribute("marker-start", head); else item.path.removeAttribute("marker-start");
    }
  };
  const box = id => {
    const g = svg("g", {class: "flow-box", "data-node": id, tabindex: 0, role: "button"});
    const rect = svg("rect", {rx: 13}), title = svg("text", {class: "flow-name", "text-anchor": "middle"}), state = svg("text", {class: "flow-word", "text-anchor": "middle"});
    const more = svg("g", {class: "flow-more"}), info = svg("g", {class: "flow-info", role: "button", tabindex: 0});
    more.append(svg("circle", {r: 10}), svg("path", {d: "M -4 0 H 4"}), svg("path", {class: "flow-plus", d: "M 0 -4 V 4"}));
    info.append(svg("rect", {width: 24, height: 20, rx: 6}), svg("text", {x: 12, y: 13, "text-anchor": "middle"}, "..."));
    g.append(rect, title, state, more, info);
    const item = {g, rect, title, state, more, info, fresh: true};
    item.layout = r => {
      more.setAttribute("transform", `translate(${r.w - 20} ${r.open ? 21 : r.h / 2})`);
      info.setAttribute("transform", `translate(${r.w - (r.open ? 62 : 32)} ${r.open ? 11 : -10})`);
      if (r.open) { title.setAttribute("text-anchor", "start"); title.replaceChildren(svg("tspan", {x: 16, y: 25}, model.nodes.get(id).name)); state.textContent = ""; return; }
      const size = boxSize(model.nodes.get(id)), word = attention[model.nodes.get(id).state] || "", top = (r.h - size.rows.length * 18 - (word ? 16 : 0)) / 2 + 14;
      title.setAttribute("text-anchor", "middle");
      title.replaceChildren(...size.rows.map((s, i) => svg("tspan", {x: r.w / 2, y: top + i * 18}, s)));
      state.setAttribute("x", r.w / 2); state.setAttribute("y", top + size.rows.length * 18); state.textContent = word;
    };
    g.addEventListener("click", e => { if (!root.dataset.dragged) onToggle(id); e.stopPropagation(); });
    g.addEventListener("keydown", e => { if (["Enter", " "].includes(e.key) && e.target === g) { e.preventDefault(); onToggle(id); } });
    g.addEventListener("mouseenter", () => { focus = id; highlight(); });
    g.addEventListener("mouseleave", () => { focus = ""; highlight(); });
    g.addEventListener("focus", () => { focus = id; highlight(); show(model.rects.get(id)); });
    g.addEventListener("blur", () => { focus = ""; highlight(); });
    const detail = e => { e.preventDefault(); e.stopPropagation(); onDetails(id); };
    info.addEventListener("click", detail);
    info.addEventListener("keydown", e => { if (["Enter", " "].includes(e.key)) detail(e); });
    (model.rects.get(id)?.open ? frames : boxes).append(g);
    items.set(id, item);
    return item;
  };
  // Keep a box on screen, zooming out only if it cannot fit.
  const show = r => {
    if (!r) return;
    const {w, h} = size(), k = Math.min(view.k, (w - 64) / r.w, (h - 64) / r.h);
    const left = r.x * k + view.x, top = r.y * k + view.y;
    const x = view.x + Math.max(0, 32 - left) - Math.max(0, left + r.w * k - (w - 32));
    const y = view.y + Math.max(0, 32 - top) - Math.max(0, top + r.h * k - (h - 32));
    if (x !== view.x || y !== view.y || k !== view.k) glide(shown, {x, y, k}, model.rects);
  };
  const zoom = (factor, px = size().w / 2, py = size().h / 2) => {
    const k = Math.max(.15, Math.min(2.5, view.k * factor)), s = k / view.k;
    view = {x: px - (px - view.x) * s, y: py - (py - view.y) * s, k}; apply();
  };
  root.addEventListener("wheel", e => {
    e.preventDefault();
    const at = root.getBoundingClientRect();
    if (e.ctrlKey || e.metaKey) zoom(Math.exp(-e.deltaY * .01), e.clientX - at.left, e.clientY - at.top);
    else { view = {...view, x: view.x - e.deltaX, y: view.y - e.deltaY}; apply(); }
  }, {passive: false});
  let drag = null;
  root.addEventListener("pointerdown", e => { if (e.button === 0) { drag = {x: e.clientX, y: e.clientY, vx: view.x, vy: view.y, id: e.pointerId}; delete root.dataset.dragged; } });
  root.addEventListener("pointermove", e => {
    if (!drag) return;
    const dx = e.clientX - drag.x, dy = e.clientY - drag.y;
    if (!root.dataset.dragged && Math.hypot(dx, dy) < 5) return;
    if (!root.dataset.dragged) { root.dataset.dragged = "yes"; root.setPointerCapture(drag.id); }
    view = {...view, x: drag.vx + dx, y: drag.vy + dy}; apply();
  });
  const release = () => { drag = null; setTimeout(() => delete root.dataset.dragged); };
  root.addEventListener("pointerup", release); root.addEventListener("pointercancel", release);
  root.addEventListener("keydown", e => {
    const step = {ArrowLeft: [48, 0], ArrowRight: [-48, 0], ArrowUp: [0, 48], ArrowDown: [0, -48]}[e.key];
    if (step) { e.preventDefault(); view = {...view, x: view.x + step[0], y: view.y + step[1]}; apply(); }
    else if (["+", "="].includes(e.key)) zoom(1.2);
    else if (e.key === "-") zoom(1 / 1.2);
    else if (e.key === "0") fit();
  });
  const fit = () => glide(shown, fitting(bounds(model.rects)), model.rects);
  const observer = new ResizeObserver(() => { if (!fitted && model.rects.size && root.clientWidth) refit(); });
  const refit = () => { fitted = true; view = fitting(bounds(model.rects)); shown = model.rects; place(shown, 1); name(shown); highlight(); apply(); };
  const update = (next, keep) => {
    const from = new Map(shown), signature = JSON.stringify([[...next.rects].map(([id, r]) => [id, r.x, r.y, r.w, r.h, next.nodes.get(id)?.state, next.nodes.get(id)?.children]), next.edges.map(e => [e.key, e.when]), keep]);
    if (signature === shape) return;
    shape = signature; model = next;
    const keepNodes = new Set(next.rects.keys());
    for (const [id, item] of items) if (!keepNodes.has(id)) { item.g.remove(); items.delete(id); }
    for (const [id, r] of next.rects) {
      const item = items.get(id) || box(id), n = next.nodes.get(id);
      const home = r.open ? frames : boxes;
      if (item.g.parentNode !== home) home.append(item.g);
      item.g.dataset.state = n.state || ""; item.g.classList.toggle("flow-group", r.open); item.g.dataset.depth = r.depth;
      item.more.style.display = n.children || r.open ? "" : "none";
      item.more.classList.toggle("opened", r.open);
      item.g.setAttribute("aria-label", n.children || r.open ? `${r.open ? "Close" : "Open"} ${n.name}` : n.name);
      if (r.open) item.g.setAttribute("aria-expanded", "true"); else item.g.removeAttribute("aria-expanded");
      if (!from.has(id)) from.set(id, {...r, ...(next.origin(id) ? {x: next.origin(id).x, y: next.origin(id).y, w: r.w, h: r.h} : {})});
    }
    for (const e of next.edges) if (!arrows.has(e.key)) {
      const path = svg("path"), label = svg("g", {class: "flow-label"}), tag = svg("rect", {rx: 5}), word = svg("text", {"text-anchor": "middle"});
      label.append(tag, word); lines.append(path); labels.append(label);
      arrows.set(e.key, {path, label, tag, word, from: e.from, to: e.to});
    }
    for (const e of next.edges) { const item = arrows.get(e.key); item.path.dataset.state = e.state || ""; item.both = e.both; }
    if (!fitted) { if (root.clientWidth) refit(); else { shown = next.rects; if (root.isConnected) observer.observe(root); } return; }
    const target = keep && next.rects.get(keep);
    let camera = view;
    // After a box opens or closes, the whole tree stays in view while it is still readable,
    // and otherwise that box does.
    const whole = fitting(bounds(next.rects));
    if (target) camera = whole.k >= READABLE ? whole : fitting(target);
    for (const item of items.values()) if (item.fresh && shown.size) item.g.style.opacity = 0;
    glide(from, camera, next.rects);
    setTimeout(() => { for (const item of items.values()) { item.fresh = false; item.g.style.opacity = ""; } if (target) show(next.rects.get(keep)); highlight(); }, still.matches ? 0 : MOVE + 20);
  };
  return {node: root, update, fit, zoom, connect() { if (model.rects.size && !fitted) observer.observe(root); }, disconnect() { observer.disconnect(); cancelAnimationFrame(frame); }};
}
