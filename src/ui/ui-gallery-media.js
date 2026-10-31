// Media previews use the presentation type supplied by the server.
import { message } from "./ui-components.js";
import { el, apiURL, icon } from "./ui.js";
export const fileURL = (entry, download = false) => apiURL("/api/media/" + entry.media_sha256, download ? {download:1} : {});
export function preview(entry, full = false) {
  if (entry.status !== "present") return message("Missing");
  if (entry.tag === "a") return el("div", "gallery-none", icon("file"));
  const media = el(entry.kind === "image" ? "img" : "video");
  const retry = el("button", "button", "Retry"); retry.type = "button"; retry.hidden = true;
  const node = el("div", "gallery-media", media, retry);
  const load = () => { retry.hidden = true; media.src = fileURL(entry); if (entry.kind === "video") media.load(); };
  media.addEventListener("error", () => { retry.hidden = false; });
  retry.addEventListener("click", e => { e.stopPropagation(); load(); });
  if (entry.kind === "image") Object.assign(media, {alt:entry.shows, loading:full ? "eager" : "lazy", decoding:"async"});
  else Object.assign(media, {controls:true, preload:"metadata", playsInline:true});
  load(); return node;
}
