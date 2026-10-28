// Shared page, card, action and state components. Screens supply content;
// selection and ledger semantics stay on the server.
import { el, icon } from "./ui.js";

export const card = (...children) => el("section", "panel card", children);
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
