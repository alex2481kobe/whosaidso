// The gallery inspector owns modal navigation, focus and playback cleanup.
// File bytes, availability and ledger changes belong to the server.
import { el, project, stamp, link, send, copyButton } from "./ui.js";
import { action, preview, fileURL, saveOriginal } from "./ui-gallery-media.js";

function removeButton(entry, said, shownProject) {
  let armed = false, timer;
  const b = action("close", "Remove from gallery", () => {
    if (!armed) {
      armed = true; b.lastChild.textContent = "Confirm removal";
      timer = setTimeout(() => { armed = false; b.lastChild.textContent = "Remove from gallery"; }, 4000);
      return;
    }
    clearTimeout(timer); armed = false; b.lastChild.textContent = "Remove from gallery";
    send("/api/gallery/remove", {project:shownProject, id:entry.id}).then(r => {
      said.textContent = `Retraction captured. Admit ${r.captured} in the CLI to remove the entry. Your file is unchanged.`;
    }).catch(e => { said.textContent = e.message; });
  }, "button danger-button");
  return b;
}
function sidebar(entry, titles, shownProject) {
  const said = el("p", "gallery-said"); said.setAttribute("role", "status");
  const copyPath = copyButton("Copy path", entry.path || "");
  copyPath.disabled = !entry.path;
  if (!entry.path) copyPath.title = "Local path UNKNOWN";
  const links = (entry.tasks || []).map(t => t.project && t.project !== shownProject ? el("span", "artifact-task", `${t.project} / ${t.record_id}`) : link(t.record_id, titles.get(t.record_id) || t.record_id, "artifact-task"));
  return el("aside", "artifact-sidebar",
    el("span", "verdict " + (entry.verdict || "unreviewed"), entry.verdict === "approved" ? "Approved" : entry.verdict === "rejected" ? "Rejected" : "Unreviewed"),
    links.length ? el("div", "artifact-section", el("h3", "", "Linked task"), links) : null,
    entry.note ? el("p", "artifact-description", entry.note) : null,
    el("details", "artifact-technical", el("summary", "", "Review details"),
      el("p", "", entry.verdict && entry.verdict !== "unreviewed" ? `${entry.verdict} by ${entry.verdict_by?.id || "UNKNOWN"}` : "No verdict recorded."), entry.verdict_note ? el("p", "", entry.verdict_note) : null),
    el("details", "artifact-technical", el("summary", "", "File details"),
      el("dl", "artifact-facts", [["Added by", entry.added_by || "UNKNOWN"], ["Added", stamp(new Date(entry.added_at))], ["File status", entry.status || "UNKNOWN"], ["Location", entry.path || "UNKNOWN"], ["SHA-256", entry.sha256 || "UNKNOWN"], ["Bytes", entry.length ?? "UNKNOWN"]].map(([k,v]) => el("div", "", el("dt", "", k), el("dd", "", String(v))))),
      copyPath, removeButton(entry, said, shownProject)),
    el("div", "artifact-detail-actions", saveOriginal(entry)), said);
}
export function inspect(entry, trigger, taskTitles, sequence = [entry]) {
  if (document.querySelector(".artifact-dialog")) return;
  const shownProject = project();
  let index = sequence.findIndex(e => e.id === entry.id);
  if (index < 0) { sequence = [entry]; index = 0; }
  const title = el("strong");
  const body = el("div", "artifact-dialog-body");
  const count = el("span", "gallery-footnote"); count.setAttribute("role", "status");
  const close = action("close", "Close", () => dialog.close());
  const previous = action("back", "Previous artifact", () => move(-1));
  const next = action("arrow", "Next artifact", () => move(1));
  const dialog = el("dialog", "artifact-dialog", el("div", "artifact-dialog-head", title, close), body, el("nav", "artifact-navigation", previous, count, next));
  let enlarged;
  const pause = () => {
    enlarged?.pause();
  };
  const draw = () => {
    pause();
    const current = sequence[index];
    title.textContent = current.title; dialog.setAttribute("aria-label", current.title);
    const content = preview(current, fileURL(current), true);
    enlarged = content.querySelector?.("video");
    body.replaceChildren(el("div", "artifact-stage", content), sidebar(current, taskTitles, shownProject));
    previous.disabled = index === 0; next.disabled = index === sequence.length - 1;
    count.textContent = `${index + 1} of ${sequence.length} on this page`;
  };
  const move = delta => { const at = index + delta; if (at >= 0 && at < sequence.length) { index = at; draw(); } };
  dialog.addEventListener("keydown", e => {
    if (["INPUT", "TEXTAREA", "VIDEO"].includes(e.target.tagName)) return;
    if (e.key === "ArrowLeft" || e.key === "ArrowRight") { e.preventDefault(); move(e.key === "ArrowLeft" ? -1 : 1); }
  });
  let touch;
  body.addEventListener("pointerdown", e => { touch = e.pointerType === "touch" && e.target.closest(".artifact-stage") && !e.target.closest("video, button, a, pre") ? {x:e.clientX, y:e.clientY, id:e.pointerId} : null; });
  body.addEventListener("pointerup", e => { if (touch && touch.id === e.pointerId && Math.abs(e.clientX - touch.x) > 60 && Math.abs(e.clientY - touch.y) < 40) move(e.clientX < touch.x ? 1 : -1); touch = null; });
  body.addEventListener("pointercancel", () => { touch = null; });
  dialog.addEventListener("click", e => { if (e.target === dialog) dialog.close(); });
  const closeOnRoute = () => dialog.close(); window.addEventListener("hashchange", closeOnRoute);
  dialog.addEventListener("close", () => {
    pause(); window.removeEventListener("hashchange", closeOnRoute); dialog.remove();
    if (trigger.isConnected) trigger.focus({preventScroll:true});
  }, {once:true});
  draw(); document.body.append(dialog); dialog.showModal(); close.focus();
}
