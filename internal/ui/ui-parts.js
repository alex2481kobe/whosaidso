// The parts every screen is built from: the hero (the greeting or title on
// the left; the owl on its rock with the light mountains, foliage and clouds
// and a handwritten line on the right), stat cards with sparklines, panel
// heads, rounded-rectangle chips and the custom dropdown menu. Screens
// decide what goes in them; nothing here reads the ledger.
import { el } from "./ui.js";
import { icon, svg } from "./ui-icons.js";

// The handwritten lines beside the owl; each screen starts at its own line
// and the choice moves on by one every day.
const lines = [
  ["Clear records,", "brighter progress."], ["Same facts.", "Brighter progress."], ["Every change", "tells a bigger story."],
  ["Test, prove,", "optimize, repeat."], ["Simple, efficient,", "reliable."],
];
const screens = { home: 0, records: 1, history: 2, detail: 3 };
export function caption(screen) {
  const day = Math.floor((Date.now() - new Date().getTimezoneOffset() * 6e4) / 864e5);
  return lines[((screens[screen] || 0) + day) % lines.length];
}

// cloud is one soft cloud whose flat base starts at x, y.
const cloud = (x, y, s) => svg("path", { class: "cloud", d: `M${x} ${y}h${36 * s}a${7 * s} ${7 * s} 0 0 0-${6 * s}-${9 * s}a${10 * s} ${10 * s} 0 0 0-${18 * s}-${3 * s}a${8 * s} ${8 * s} 0 0 0-${12 * s} ${12 * s}z` });
// tuft is a small clump of light foliage at the foot of the hills.
const tuft = (x, y, s) => svg("path", { class: "tuft", d: `M${x} ${y}q${-2 * s} ${-8 * s} ${-7 * s} ${-12 * s}M${x} ${y}q${-1 * s} ${-10 * s} ${1 * s} ${-16 * s}M${x} ${y}q${3 * s} ${-7 * s} ${8 * s} ${-11 * s}M${x + 5 * s} ${y}q${2 * s} ${-5 * s} ${6 * s} ${-7 * s}` });
// sprig is a stem with leaves beside the rock; flip mirrors it.
function sprig(x, y, h, flip) {
  const leaf = (lx, ly, angle, size) => svg("path", { class: "leaf", transform: `translate(${lx} ${ly}) rotate(${angle}) scale(${size})`, d: "M0 0c5-2 9-7 9-13c-5 2-9 7-9 13z" });
  const f = flip ? -1 : 1;
  return svg("g", {}, svg("path", { class: "stem", d: `M${x} ${y}q${-3 * f} ${-h / 2} ${2 * f} ${-h}` }),
    leaf(x + 1 * f, y - h * 0.3, flip ? -70 : 10, 1.05), leaf(x - 1 * f, y - h * 0.5, flip ? 20 : -100, 0.95),
    leaf(x + 1 * f, y - h * 0.72, flip ? -60 : 0, 0.85), leaf(x, y - h * 0.9, flip ? 30 : -110, 0.7));
}

// scenery is the drawing behind the owl: far and near hills fading into the
// page, clouds, light foliage, two sprigs and the rock the owl stands on.
function scenery() {
  const fade = svg("linearGradient", { id: "hero-fade", x1: 0, y1: 0, x2: 0, y2: 1 },
    svg("stop", { offset: 0, class: "fade-top" }), svg("stop", { offset: 0.45, class: "fade-top" }), svg("stop", { offset: 1, class: "fade-bottom" }));
  const fadeNear = svg("linearGradient", { id: "hero-fade-near", x1: 0, y1: 0, x2: 0, y2: 1 },
    svg("stop", { offset: 0, class: "fade-near" }), svg("stop", { offset: 0.55, class: "fade-near" }), svg("stop", { offset: 1, class: "fade-bottom" }));
  return svg("svg", { class: "scenery", viewBox: "0 0 860 150", preserveAspectRatio: "xMaxYMax meet", "aria-hidden": "true" },
    svg("defs", {}, fade, fadeNear),
    svg("path", { class: "hill far", fill: "url(#hero-fade)", d: "M0 150C60 138 110 128 150 126C185 124 205 128 230 136L230 150z" }),
    svg("path", { class: "hill", fill: "url(#hero-fade)", d: "M90 150C150 128 190 92 240 70C268 58 290 54 312 60C352 72 392 104 450 120C520 138 640 134 860 140L860 150z" }),
    svg("path", { class: "hill near", fill: "url(#hero-fade-near)", d: "M360 150C430 126 480 88 548 78C600 71 640 88 690 106C745 124 810 130 860 132L860 150z" }),
    cloud(262, 48, 0.9), cloud(372, 76, 0.75), cloud(700, 44, 0.8), cloud(810, 60, 0.7),
    tuft(96, 148, 1), tuft(232, 146, 0.9), tuft(262, 148, 0.7), tuft(606, 148, 0.8), tuft(790, 146, 1.1),
    sprig(418, 146, 42, false), sprig(560, 146, 50, true),
    svg("path", { class: "rock", d: "M408 150C412 133 432 120 462 116C492 112 522 116 542 126C556 133 564 142 568 150z" }),
    svg("path", { class: "rock-top", d: "M428 131C443 121 471 116 500 117C521 118 536 123 546 130C520 125 470 124 428 131z" }),
    svg("image", { href: "/owl.png", x: 458, y: 21, width: 60, height: 97 }));
}

// hero is a screen's top band. lead is [before, bold, after]; mark is an
// emoji or an icon shown after the title.
export function hero(screen, title, mark, lead, sub) {
  const [one, two] = caption(screen);
  return el("section", "hero",
    el("div", "hero-text",
      el("h1", "", el("span", "", title), mark ? el("span", "hero-mark", mark) : null),
      el("p", "hero-lead", lead[0], el("strong", "", lead[1]), lead[2] || ""),
      sub ? el("p", "hero-sub", sub) : null),
    el("div", "hero-art", scenery(), el("p", "hero-caption", el("span", "", one), el("span", "", two))));
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
