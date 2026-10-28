// The parts every screen is built from: the hero (a screen's title), stat
// cards with sparklines, panel heads, rounded-rectangle chips and the custom
// dropdown menu. Screens decide what goes in them; nothing here reads the
// ledger.
import { el } from "./ui.js";
import { icon, svg } from "./ui-icons.js";

// hero is a screen's title.
export function hero(title) {
  return el("header", "hero", el("h1", "", title));
}

// sparkline draws counts per day as a line, with a soft area when asked.
export function sparkline(values, tone, area, label) {
  const w = 120, h = 34, max = Math.max(1, ...values);
  const pts = values.map((v, i) => [(i * w) / Math.max(1, values.length - 1), h - 3 - (v / max) * (h - 8)]);
  // a smooth curve through the points (Catmull-Rom as cubic Béziers)
  let d = `M${pts[0][0]} ${pts[0][1]}`;
  for (let i = 0; i < pts.length - 1; i++) {
    const [p0, p1, p2, p3] = [pts[Math.max(0, i - 1)], pts[i], pts[i + 1], pts[Math.min(pts.length - 1, i + 2)]];
    d += `C${p1[0] + (p2[0] - p0[0]) / 6} ${p1[1] + (p2[1] - p0[1]) / 6} ${p2[0] - (p3[0] - p1[0]) / 6} ${p2[1] - (p3[1] - p1[1]) / 6} ${p2[0]} ${p2[1]}`;
  }
  const node = svg("svg", { class: "spark tone-stroke " + tone, viewBox: `0 0 ${w} ${h}`, preserveAspectRatio: "none", role: "img", "aria-label": label },
    svg("title", {}, label));
  if (area) node.append(svg("path", { class: "spark-area", d: d + `L${w} ${h}L0 ${h}z` }));
  node.append(svg("path", { class: "spark-line", d, "vector-effect": "non-scaling-stroke" }));
  return node;
}

// statCard is one counted fact: an icon tile, the number and its label,
// then an optional sparkline and a one-line note. With onPick it is a
// button; selected marks the card whose list is shown.
export function statCard({ glyph, tone, value, label, note, noteTone = "", spark, onPick, selected, layout = "" }) {
  const top = el("div", "stat-top", el("span", "tile tone-tint " + tone, icon(glyph)),
    el("div", "stat-words", el("span", "stat-value", String(value)), el("span", "stat-label", label),
      layout === "compact" && note ? el("span", "stat-note " + noteTone, note) : null));
  const card = el(onPick ? "button" : "div", "stat " + layout, top, spark || null,
    layout !== "compact" && note ? el("span", "stat-note " + noteTone, note) : null);
  if (onPick) {
    card.type = "button";
    card.setAttribute("aria-pressed", String(Boolean(selected)));
    card.addEventListener("click", onPick);
  }
  return card;
}

// head is a panel's title row: an icon, the title, an optional subtitle and
// whatever sits at its right end.
export function head(glyph, tone, title, sub, ...end) {
  return el("header", "panel-head", el("span", "head-icon tone-text " + tone, icon(glyph)),
    el("div", "head-words", el("h2", "", title), sub ? el("p", "head-sub", sub) : null), el("div", "head-end", end));
}

// more is a quiet "View all ->" style link or button.
export function more(text, onPick) {
  const b = el("button", "more", el("span", "", text), icon("arrow"));
  b.type = "button";
  b.addEventListener("click", onPick);
  return b;
}

// chip is a rounded-rectangle toggle in a chip row.
export function chip(text, selected, onPick, lead) {
  const b = el("button", "chip", lead || null, el("span", "", text));
  b.type = "button";
  b.setAttribute("aria-pressed", String(selected));
  b.addEventListener("click", onPick);
  return b;
}

// dropdown is the custom menu: a button showing the current choice and a
// list of options (value, text, extra) that closes on a pick, an outside
// click, Escape or a scroll. One menu is open at a time.
let current = null;
const close = () => { if (current) { const c = current; current = null; c(); } };
document.addEventListener("click", close);
document.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
window.addEventListener("resize", close);

const menuRow = (text, extra, selected, onPick) => {
  const row = el("button", "menu-row", el("span", "", text), extra !== undefined ? el("span", "menu-extra", String(extra)) : null);
  row.type = "button";
  row.setAttribute("role", "option");
  row.setAttribute("aria-selected", String(selected));
  row.addEventListener("click", (e) => { e.stopPropagation(); close(); onPick(); });
  return row;
};

// place keeps an open menu inside the window: it slides left or right until
// both edges are 8px in (CSS caps its width to the window), and it opens
// upwards when there is more room above its button than below, scrolling
// inside whatever height that side has.
export function place(list, anchor) {
  list.classList.remove("up");
  list.style.transform = "";
  list.style.maxHeight = "";
  const r = list.getBoundingClientRect();
  const shift = r.left + Math.min(0, innerWidth - 8 - r.right) < 8 ? 8 - r.left : Math.min(0, innerWidth - 8 - r.right);
  if (shift) list.style.transform = `translateX(${shift}px)`;
  const a = anchor.getBoundingClientRect();
  const below = innerHeight - a.bottom - 14, above = a.top - 14;
  const up = r.height > below && above > below;
  list.classList.toggle("up", up);
  list.style.maxHeight = Math.max(0, up ? above : below) + "px";
}

// placeholder, when given, is the button's word while no option is chosen.
export function dropdown({ glyph, options, value, onPick, prefix = "", className = "", placeholder = "" }) {
  const chosen = options.find((o) => o[0] === value) || (placeholder ? [null, placeholder] : options[0]);
  const button = el("button", "select", glyph ? icon(glyph) : null, el("span", "", prefix + chosen[1]), icon("down", "caret"));
  button.type = "button";
  button.setAttribute("aria-haspopup", "listbox");
  button.setAttribute("aria-expanded", "false");
  const list = el("div", "menu", options.map(([v, text, extra]) => menuRow(text, extra, v === chosen[0], () => onPick(v))));
  list.setAttribute("role", "listbox");
  list.hidden = true;
  button.addEventListener("click", (e) => {
    e.stopPropagation();
    const opening = list.hidden;
    close();
    if (!opening) return;
    list.hidden = false;
    place(list, button);
    button.setAttribute("aria-expanded", "true");
    current = () => { list.hidden = true; button.setAttribute("aria-expanded", "false"); };
  });
  return el("div", "dropdown " + className, button, list);
}
