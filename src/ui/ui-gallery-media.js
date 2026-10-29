// Media presentation ported from v0.2.1 internal/ui/ui-gallery-media.js.
import { message } from "./ui-components.js";
import { el, apiURL, icon } from "./ui.js";
export const fileURL = (entry, download = false) => apiURL("/api/media/" + entry.media_sha256, download ? {download:1} : {});
export function preview(entry, full = false) {
  if (entry.status !== "present") return message("Media not on this machine");
  if (entry.kind === "document") return el("div", "gallery-none", icon("file"), "PDF document");
  const media = el(entry.kind === "image" ? "img" : "video");
  const said = el("span", "gallery-load-status", "Loading"); said.setAttribute("role", "status");
  const retry = el("button", "button", icon("retry"), "Retry preview"); retry.type = "button"; retry.hidden = true;
  const node = el("div", "gallery-media", media, said, retry);
  const load = () => { said.textContent = "Loading"; retry.hidden = true; media.src = fileURL(entry); if (entry.kind === "video") media.load(); };
  media.addEventListener(entry.kind === "image" ? "load" : "loadedmetadata", () => { said.textContent = ""; retry.hidden = true; });
  media.addEventListener("error", () => { said.textContent = "Preview unavailable. Retry or open the original."; retry.hidden = false; });
  retry.addEventListener("click", e => { e.stopPropagation(); load(); });
  if (entry.kind === "image") Object.assign(media, {alt:entry.shows, loading:full ? "eager" : "lazy", decoding:"async"});
  else Object.assign(media, {controls:true, preload:"metadata", playsInline:true});
  load(); return node;
}
