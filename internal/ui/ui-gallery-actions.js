// Gallery file actions share one icon row across cards and the item view.
// The server verifies files and admits retractions; the browser reports errors.
import { el, icon, send, project } from "./ui.js";
import { fileURL } from "./ui-gallery-media.js";

export function iconAction(glyph, label, fn) {
  const b = el("button", "button icon-button", icon(glyph));
  b.type = "button"; b.title = label; b.setAttribute("aria-label", label);
  b.addEventListener("click", fn);
  return b;
}
const reveal = (entry, shown) => send("/api/gallery/reveal", {project:shown, id:entry.id});
export function titleButton(entry, said) {
  const shown = project();
  const b = el("button", "artifact-title-button", entry.title);
  b.type = "button"; b.title = "Reveal in file manager";
  b.addEventListener("click", () => reveal(entry, shown).catch(error => {said.textContent = error.message;}));
  return b;
}
export function galleryActions(entry, said, removed) {
  const shown = project();
  const run = async (button, fn) => {
    button.disabled = true; said.textContent = "";
    try { await fn(); } catch (error) { said.textContent = error.message; }
    finally { button.disabled = false; }
  };
  const show = iconAction("open", "Reveal in file manager", () => run(show, () => reveal(entry, shown)));
  const copy = iconAction("copy", "Copy path", () => run(copy, () => navigator.clipboard.writeText(entry.path)));
  copy.disabled = !entry.path;
  const download = el("a", "button icon-button", icon("download"));
  download.href = fileURL(entry, "download"); download.title = "Download";
  download.setAttribute("aria-label", "Download"); download.setAttribute("download", "");
  const remove = iconAction("trash", "Delete", () => run(remove, async () => {
    await send("/api/gallery/remove", {project:shown, id:entry.id}); removed(entry.id);
  }));
  remove.classList.add("danger-button");
  return el("div", "gallery-actions", show, copy, download, remove);
}
