// Small shared pieces: a card, an empty or error state, an icon-only button.
import { el, icon } from "./ui.js";

export const card = (...children) => el("section", "card", children);
export function message(text, kind = "empty") {
  const node = el("div", "content-state " + kind, text);
  node.setAttribute("role", kind === "error" ? "alert" : "status");
  return node;
}
export function iconButton(glyph, label, fn) {
  const button = el("button", "icon-button", icon(glyph));
  button.type = "button"; button.title = label; button.setAttribute("aria-label", label);
  button.addEventListener("click", fn);
  return button;
}
