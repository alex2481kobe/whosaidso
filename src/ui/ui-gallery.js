// The newest pieces, with their goal when one was recorded.
import { el, api } from "./ui.js";
import { card } from "./ui-components.js";
import { preview, fileURL } from "./ui-gallery-media.js";
import { titleButton } from "./ui-gallery-actions.js";
import { inspect } from "./ui-gallery-inspect.js";
function galleryCard(entry) {
  const stage = el(entry.tag === "a" ? "a" : "div", "artifact-preview", preview(entry));
  stage.setAttribute("aria-label", "Open " + entry.shows);
  if (entry.tag === "a") {
    stage.href = fileURL(entry); stage.target = "_blank"; stage.rel = "noopener";
  } else {
    stage.tabIndex = 0; stage.setAttribute("role", "button");
    stage.addEventListener("click", e => { if (!e.target.closest("button,video")) inspect(entry, stage); });
    stage.addEventListener("keydown", e => { if (e.target === stage && ["Enter"," "].includes(e.key)) { e.preventDefault(); inspect(entry, stage); } });
  }
  const body = el("div", "gallery-card-body", el("h2", "gallery-title", titleButton(entry)));
  if (entry.goal) body.append(el("span", "muted", entry.goal_title || entry.goal));
  const node = card(stage, body);
  node.classList.add("gallery-card"); return node;
}
export function galleryScreen() {
  const requested = new URLSearchParams(location.hash.split("?")[1] || "").get("item");
  const cards = el("div", "gallery-cards");
  let nodes = new Map(), shown = "", opened = false;
  return { node: el("div", "screen", cards), async load() {
    const data = await api("/api/gallery");
    // Reuse cards so a playing video survives an update.
    const keys = data.entries.map(e => e.id + e.status + e.goal_title);
    if (keys.join() !== shown) {
      shown = keys.join();
      nodes = new Map(data.entries.map((e, i) => [keys[i], nodes.get(keys[i]) || galleryCard(e)]));
      cards.replaceChildren(...nodes.values());
    }
    const index = data.entries.findIndex(e => e.id === requested);
    const trigger = cards.children[index]?.querySelector(".artifact-preview[role=button]");
    if (trigger && !opened) requestAnimationFrame(() => {
      if (trigger.isConnected && !opened) { opened = true; inspect(data.entries[index], trigger); }
    });
    return data.version;
  } };
}
