// Ported from v0.2.1 internal/ui/ui-parts.js; adapted for the read-only Rust viewer.
// Workspace dropdowns own menu placement and keyboard selection. Page frames,
// cards and content states belong to ui-components.js.
import { el } from "./ui.js";
import { icon } from "./ui-icons.js";

// dropdown is the custom menu: a button showing the current choice and a
// list of options (value, text, extra) that closes on a pick, an outside
// click, Escape or a scroll. One menu is open at a time.
let current = null;
const close = () => { if (current) { const c = current; current = null; c(); } };
document.addEventListener("click", close);
document.addEventListener("keydown", (e) => { if (e.key === "Escape") close(); });
window.addEventListener("resize", close);

const menuRow = (text, extra, selected, onPick) => {
  const row = el("button", "menu-row", el("span", "", text), extra != null ? el("span", "menu-extra", String(extra)) : null);
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
