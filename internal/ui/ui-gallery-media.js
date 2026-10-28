// Gallery media presentation and recovery belong here. Ledger availability and
// byte verification are server responsibilities; image originals load on open.
import { el, apiURL, project, icon } from "./ui.js";

export function action(glyph, label, fn, cls = "button") {
  const b = el("button", cls, icon(glyph), el("span", "", label));
  b.type = "button";
  b.addEventListener("click", fn);
  return b;
}
export const fileURL = (entry, kind = "file") => apiURL("/api/gallery/" + kind, {project:project(), id:entry.id, cap:entry.cap});
export function saveOriginal(entry) {
  if (!entry.kind) {
    const unavailable = action("file", "Save unavailable", () => {}, "button artifact-save");
    unavailable.disabled = true; unavailable.title = "This file type is not served. Use Copy path in file details.";
    return unavailable;
  }
  const a = el("a", "button artifact-save", icon("down"), "Save original");
  a.href = fileURL(entry, "download");
  a.setAttribute("download", "");
  a.addEventListener("click", e => e.stopPropagation());
  return a;
}
function whenVisible(node, load, full) {
 if (full || typeof IntersectionObserver === "undefined") {load(); return;}
 const observer = new IntersectionObserver(entries => {
  if (entries.some(entry => entry.isIntersecting)) {observer.disconnect(); load();}
 }, {root:document.getElementById("main"), rootMargin:"0px"});
 observer.observe(node);
}
export function preview(entry, src, full = false) {
  if (entry.status !== "present" && !(full && entry.status === "unknown")) return el("div", "gallery-none", icon("file"), el("strong", "", entry.status === "changed" ? "File changed" : entry.status === "missing" ? "File missing" : "File status UNKNOWN"), el("span", "", entry.status_reason || "Open file details for its recorded location."));
  if (entry.kind === "image" || entry.kind === "video") {
    if (entry.kind === "video" && !full) return el("div", "gallery-none", icon("play"), "Open video");
    const media = el(entry.kind === "image" ? "img" : "video");
    const said = el("span", "gallery-load-status", "Loading preview…");
    said.setAttribute("role", "status");
    const retry = action("refresh", "Retry preview", e => { e.stopPropagation(); load(); });
    retry.hidden = true;
    const node = el("div", "gallery-media", media, said, retry);
    const load = () => { said.textContent = "Loading preview…"; retry.hidden = true; media.hidden = false; media.src = src; if (entry.kind === "video") media.load(); };
    media.addEventListener(entry.kind === "image" ? "load" : "loadeddata", () => { said.textContent = ""; retry.hidden = true; });
    media.addEventListener("error", () => { media.hidden = true; said.textContent = "Preview unavailable. Retry or open the original."; retry.hidden = false; });
    if (entry.kind === "image") Object.assign(media, {alt:entry.title, loading:"eager", fetchPriority:full ? "auto" : "high", decoding:"async"});
    else Object.assign(media, {controls:true, preload:"metadata", playsInline:true});
    whenVisible(node, load, full);
    return node;
  }
  if (entry.kind === "text") {
    const pre = el("pre", "gallery-text", "Loading preview…");
    pre.tabIndex = 0; pre.setAttribute("aria-label", entry.title + " text preview");
    const retry = action("refresh", "Retry preview", e => { e.stopPropagation(); load(); });
    const load = () => {
      pre.textContent = "Loading preview…";
      fetch(src).then(async r => {
        if (!r.ok) throw Error(`${r.status} ${r.statusText}`);
        const cut = r.headers.get("X-WhoSaidSo-Truncated");
        pre.textContent = (await r.text()) + (cut ? `\n[first ${Math.round(cut / 1024)} KiB shown]` : "");
      }).catch(e => { pre.textContent = "Could not read file: " + e.message; pre.append(retry); });
    };
    whenVisible(pre, load, full);
    return pre;
  }
  return el("div", "gallery-none", icon("file"), "Preview unavailable");
}
