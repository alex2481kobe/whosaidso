// The item view contains media, its revealing title, and the shared file actions.
// Admission and byte checks stay on the server; close stops video playback.
import { el } from "./ui.js";
import { preview, fileURL } from "./ui-gallery-media.js";
import { galleryActions, titleButton, iconAction } from "./ui-gallery-actions.js";

export function inspect(entry, trigger, removed = () => {}) {
  if (document.querySelector(".artifact-dialog")) return;
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const close = iconAction("close", "Close", () => dialog.close());
  const content = entry.kind === "image" || entry.kind === "video"
    ? preview(entry, fileURL(entry), true) : el("p", "gallery-none", "Preview unavailable");
  const dialog = el("dialog", "artifact-dialog",
    el("div", "artifact-dialog-head", titleButton(entry, said), close),
    el("div", "artifact-dialog-body", el("div", "artifact-stage", content)),
    el("div", "artifact-dialog-actions", galleryActions(entry, said, id => {removed(id); dialog.close();}), said));
  dialog.setAttribute("aria-label", entry.title);
  dialog.addEventListener("click", e => {if (e.target === dialog) dialog.close();});
  const closeOnRoute = () => dialog.close();
  window.addEventListener("hashchange", closeOnRoute);
  dialog.addEventListener("close", () => {
    content.querySelector?.("video")?.pause();
    window.removeEventListener("hashchange", closeOnRoute); dialog.remove();
    if (trigger.isConnected) trigger.focus({preventScroll:true});
  }, {once:true});
  document.body.append(dialog); dialog.showModal(); close.focus();
}
