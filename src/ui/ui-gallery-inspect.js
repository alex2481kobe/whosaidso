// The reference modal keeps keyboard dismissal and restores focus on close.
import { el } from "./ui.js";
import { iconButton } from "./ui-components.js";
import { preview } from "./ui-gallery-media.js";
import { galleryActions, titleButton } from "./ui-gallery-actions.js";
export function inspect(entry, trigger) {
  if (document.querySelector(".artifact-dialog")) return;
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const close = iconButton("close", "Close", () => dialog.close());
  const content = preview(entry, true);
  const dialog = el("dialog", "artifact-dialog", el("div", "artifact-dialog-head", titleButton(entry), close),
    el("div", "artifact-dialog-body", el("div", "artifact-stage", content)), el("div", "artifact-dialog-actions", galleryActions(entry, said), said));
  dialog.setAttribute("aria-label", entry.shows);
  dialog.addEventListener("click", e => { if (e.target === dialog) dialog.close(); });
  const closeOnRoute = () => dialog.close(); window.addEventListener("hashchange", closeOnRoute);
  dialog.addEventListener("close", () => {
    content.querySelector?.("video")?.pause(); window.removeEventListener("hashchange", closeOnRoute); dialog.remove();
    if (trigger.isConnected) trigger.focus({preventScroll:true});
  }, {once:true});
  document.body.append(dialog); dialog.showModal(); close.focus();
}
