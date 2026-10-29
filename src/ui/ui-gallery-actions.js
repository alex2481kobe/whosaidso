// The reference action row, restricted to opening, copying and downloading.
import { el, icon } from "./ui.js";
import { fileURL } from "./ui-gallery-media.js";
export function titleButton(entry) {
  const a = el("a", "artifact-title-button", entry.shows); a.href = fileURL(entry); a.target = "_blank"; a.rel = "noopener"; a.title = "Open full size"; return a;
}
export function galleryActions(entry, said) {
  const open = el("a", "button icon-button", icon("open"));
  open.href = fileURL(entry); open.target = "_blank"; open.rel = "noopener"; open.title = "Open full size"; open.setAttribute("aria-label", open.title);
  const copy = el("button", "button icon-button", icon("copy"));
  copy.type = "button"; copy.title = "Copy path"; copy.setAttribute("aria-label", copy.title);
  copy.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(entry.path); said.textContent = "Path copied"; }
    catch { said.textContent = "Could not copy path"; }
    setTimeout(() => { said.textContent = ""; }, 1600);
  });
  const download = el("a", "button icon-button", icon("download"));
  download.href = fileURL(entry, true); download.title = "Download"; download.setAttribute("aria-label", "Download"); download.setAttribute("download", "");
  return el("div", "gallery-actions", open, copy, download);
}
