// Links to a gallery item: its title opens it full size; the dialog also offers a download.
import { el, icon } from "./ui.js";
import { fileURL } from "./ui-gallery-media.js";
export function titleButton(entry) {
  const a = el("a", "artifact-title-button", entry.shows); a.href = fileURL(entry); a.target = "_blank"; a.rel = "noopener"; return a;
}
export function downloadButton(entry) {
  const a = el("a", "button icon-button", icon("download"));
  a.href = fileURL(entry, true); a.title = "Download"; a.setAttribute("aria-label", "Download"); a.setAttribute("download", "");
  return a;
}
