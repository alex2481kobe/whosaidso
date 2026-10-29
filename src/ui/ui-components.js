// Ported from v0.2.1 internal/ui/ui-components.js; adapted for the read-only Rust viewer.
// Shared page, card, action and state components. Screens supply content;
// selection and ledger semantics stay on the server.
import { el, icon } from "./ui.js";

export const card = (...children) => el("section", "panel card", children);
// listCard is the card every list screen uses; a heading, when given, is the
// first child and is padded like the rows beneath it. listRow is its row.
export const listCard = (heading) => el("section", "panel card browser-rows", heading ? el("h2", "", heading) : null);
export const listRow = (...cells) => el("div", "row-line browser-row", cells);
// countsLine is the one counts line on every list screen; fillCounts shows
// each count as a sentence-case label and a stronger number. lead nodes (a
// navigation link) come first on the line.
export const countsLine = () => el("div", "counts-line");
export function fillCounts(line, counts, lead = []) {
  line.replaceChildren(...lead, ...Object.entries(counts || {}).filter(([, n]) => n != null).map(([name, n]) =>
    el("span", "counts-item", el("span", "counts-label", name.charAt(0).toUpperCase() + name.slice(1).toLowerCase()), " ", el("span", "counts-num", n))));
}
export function message(text, kind = "empty") {
  const node = el("div", "content-state " + kind, text);
  node.setAttribute("role", kind === "error" ? "alert" : "status");
  return node;
}
export function iconButton(glyph, label, fn) {
  const button = el("button", "button icon-button", icon(glyph));
  button.type = "button"; button.title = label; button.setAttribute("aria-label", label);
  button.addEventListener("click", fn);
  return button;
}
export function pageFrame(node, title) {
  const heading = node.querySelector("h1");
  if (heading) { title = heading.textContent || title; heading.remove(); }
  node.classList.add("page-content");
  return el("div", "screen viewer-screen", el("header", "page-header", el("h1", "", title), document.getElementById("theme-toggle")), node);
}
