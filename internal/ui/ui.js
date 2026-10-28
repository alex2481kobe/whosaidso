// Shared viewer data and DOM helpers live here. Chrome and routing belong to
// ui-shell.js; each screen owns its rendering and bounded refresh.
import { icon } from "./ui-icons.js";
import { card, message } from "./ui-components.js";
import { startViewer } from "./ui-shell.js";

export { icon };

export const main = document.getElementById("main");
// switches counts project changes and renders counts navigations: an answer
// or a screen started before a newer one is dropped, never drawn over it.
export const state = { projects: [], project: "", mark: "", cache: new Map(), switches: 0, renders: 0 };

// project is the id of the project on screen.
export const project = () => state.project;

// ---- data: the session cookie the launch URL was traded for goes with every
// request, and the key that came with it in the URL fragment goes in a header
// (a gallery file's URL carries its own capability instead). The key is kept
// in this tab's sessionStorage, which no other origin, port included, reads.
const key = (() => {
  const given = /^#key=([0-9a-f]+)$/.exec(location.hash);
  let k = given ? given[1] : "";
  try { if (k) sessionStorage.setItem("whosaidso-key", k); else k = sessionStorage.getItem("whosaidso-key") || ""; } catch {}
  if (given) history.replaceState(null, "", "/#/");
  return k;
})();
export const apiURL = (path, params = {}) => {
  const query = new URLSearchParams(params).toString();
  return path + (query ? "?" + query : "");
};
export const api = (path, params = {}, signal) => answer(fetch(apiURL(path, params), { headers: { "X-WhoSaidSo-Key": key }, signal }));
// send is a gallery POST: Reveal in Finder or Remove from gallery.
export const send = (path, params = {}) => answer(fetch(apiURL(path, params), { method: "POST", headers: { "X-WhoSaidSo-Key": key } }));
async function answer(request) {
  const response = await request;
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error?.message || `${response.status} ${response.statusText}`);
  return body;
}

// view answers one view for the selected project, once per mark (see markOf).
export function view(name, params = {}) {
  const key = JSON.stringify([state.project, state.mark, name, params]);
  if (!state.cache.has(key)) state.cache.set(key, api("/api/view", { project: state.project, view: name, ...params }).catch((e) => { state.cache.delete(key); throw e; }));
  return state.cache.get(key);
}

// times maps each ledger event to the time WhoSaidSo captured its packet, as
// the bundle's review.admit records it; a review's own row takes the time of
// the packets it reviewed. Unknown stays unknown.
export async function times(id) {
  const [answer, history] = await Promise.all([api("/api/times", {project:project(), id}), view("history", {id})]);
  if (!Number.isSafeInteger(answer.watermark?.sequence) || answer.watermark.sequence < 1 || !answer.watermark.head?.command_id || JSON.stringify(answer.watermark) !== JSON.stringify(history.watermark)) return () => null;
  return origin => {
    const at = answer.times[`${origin?.sequence}:${origin?.event_index}`];
    return at?.state === "known" ? new Date(at.value) : null;
  };
}

// fullTitle is a record's whole semantic text, the text its label is cut
// from (a label stops at its first line and 80 characters); the label when
// the record carries none.
export function fullTitle(record) {
  const f = record.fact || {};
  const text = f.task?.intent || f.claim?.assertion || f.decision?.question || f.instrument?.question_answered || "";
  return text.trim() || record.label;
}

// titles names every current record in full and every admitted source.
export async function titles(id) {
  const show = await view("show", id ? {id} : {});
  const names = new Map(show.records.map((r) => [r.ref.record_id, fullTitle(r)]));
  for (const s of show.sources || []) names.set(s.intake.source_id, sourceTitle(s));
  return names;
}
export const sourceTitle = (s) => `${actorName(s.intake.speaker)} said, ${s.intake.length} bytes`;

// ---- DOM helpers: text only, never HTML from the ledger
export function el(tag, className = "", ...children) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : String(child));
  }
  return node;
}
// tablePanel keeps a table's header row OUTSIDE its scroller: the header stays inside the panel's rounded
// frame and only the rows scroll beneath it. Both tables share one colgroup and one reserved scrollbar
// gutter, so their columns line up.
export function tablePanel(className, headers, widths, bodies, empty) {
  const cols = () => el("colgroup", "", widths.map((w) => { const c = el("col"); if (w) c.style.width = w; return c; }));
  const head = el("table", "table " + className, cols(), el("thead", "", el("tr", "", headers.map((h) => el("th", "", h)))));
  return el("div", "table-panel", el("div", "table-head", head),
    el("div", "table-scroll", el("table", "table " + className, cols(), bodies), empty));
}
export const mono = (text, className = "") => el("span", "mono " + className, text);
export function link(id, text = id, className = "link") {
  const a = el("a", className, text);
  a.href = "#/record/" + encodeURIComponent(id);
  return a;
}

// copyButton is a real button that copies text and says so for a moment.
export function copyButton(label, text) {
  const words = el("span", "", label);
  const button = el("button", "button", icon("copy"), words);
  button.type = "button";
  button.title = text;
  button.addEventListener("click", async (e) => {
    e.stopPropagation();
    try { await navigator.clipboard.writeText(text); words.textContent = "Copied"; button.classList.add("copied"); }
    catch { words.textContent = "Copy failed"; }
    setTimeout(() => { words.textContent = label; button.classList.remove("copied"); }, 1400);
  });
  return button;
}

// openButton is a link to a record drawn as a button.
export function openButton(id) {
  const a = link(id, "", "button");
  a.append(icon("open"), el("span", "", "Open record"));
  a.addEventListener("click", (e) => e.stopPropagation());
  return a;
}

// facts is a labelled list of [label, value] pairs; empty values are left out.
export function facts(pairs, className = "facts") {
  return el("dl", className, pairs.filter(([, v]) => v !== null && v !== undefined && v !== "")
    .map(([k, v]) => el("div", "", el("dt", "", k), el("dd", "", v))));
}

// disclose wires a button to open and close one detail. Rows that are
// alternatives share a list, and the list holds one open row at a time.
export function disclose(button, detail, row) {
  detail.hidden = true;
  button.setAttribute("aria-expanded", "false");
  if (button.tagName !== "BUTTON") {
    button.tabIndex = 0;
    button.addEventListener("keydown", (e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); button.click(); } });
  }
  button.addEventListener("click", (e) => {
    e.stopPropagation();
    const opening = detail.hidden;
    for (const open of row.parentElement?.querySelectorAll(":scope > .open") || []) open.dispatchEvent(new Event("collapse"));
    detail.hidden = !opening;
    row.classList.toggle("open", opening);
    button.setAttribute("aria-expanded", String(opening));
  });
  row.addEventListener("collapse", () => {
    detail.hidden = true;
    row.classList.remove("open");
    button.setAttribute("aria-expanded", "false");
  });
}

// item is one collapsed line: a title, a short value on the right, and the
// detail it opens on click.
export function item(title, value, detail, tone = "") {
  const head = el("button", "item-head", icon("chevron"), el("span", "item-title", title),
    value ? el("span", "item-value tone-text " + tone, value) : null);
  head.type = "button";
  const body = el("div", "item-body", detail);
  const li = el("li", "item", head, body);
  disclose(head, body, li);
  return li;
}

// section is one titled panel whose list scrolls inside itself.
export function section(title, items) {
  return card(el("h2", "", el("span", "", title), el("span", "count", String(items.length))),
    el("div", "panel-body", items.length ? el("ul", "items", items) : message("No entries.")));
}

// ---- words: the ledger's values, labelled for people
export function actorName(actor) {
  if (!actor) return "UNKNOWN";
  if (typeof actor === "string") return actor;
  return actor.id || "UNKNOWN";
}
const monthDay = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
const clock = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
const fullDay = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" });
export const stamp = (d) => (d ? `${fullDay.format(d)} ${clock.format(d)}` : "UNKNOWN");
export const when = (d) => (d ? `${monthDay.format(d)}, ${clock.format(d)}` : "UNKNOWN");
export function since(d) {
  if (!d) return "since UNKNOWN";
  return new Date().toDateString() === d.toDateString() ? "since " + clock.format(d) : "since " + monthDay.format(d);
}
export const titleCase = (s) => String(s ?? "").toLowerCase().replace(/(^|[\s-])\S/g, (c) => c.toUpperCase());

// status is a record's real status, labelled, with the tone that colours it.
export function status(record) {
  if (record.task) {
    const t = record.task;
    if (t.status === "IN FLIGHT") return ["In progress", "blue"];
    if (t.status === "READY") return ["Ready", "amber"];
    if (t.status === "CLOSED") return [t.outcome === "success" ? "Closed" : "Closed, " + t.outcome, "muted"];
    if (t.status === "BLOCKED") {
      const awaiting = (t.reasons || []).length > 0 && t.reasons.every((r) => r.kind === "awaiting-acceptance");
      return awaiting ? ["Awaiting acceptance", "amber"] : ["Blocked", "red"];
    }
    return [t.status, "muted"];
  }
  if (record.claim) {
    const s = record.claim.status;
    return [titleCase(s), { PROVEN: "green", REFUTED: "red", MEASURED: "blue" }[s] || "muted"];
  }
  if (record.decision) return [titleCase(record.decision.status), record.decision.status === "OPEN" ? "amber" : "green"];
  if (record.instrument) {
    // Active means no trust withdrawal is recorded, not that trust is known.
    const withdrawn = (record.instrument.withdrawals || []).length > 0;
    const validated = record.instrument.validation?.state === "KNOWN" ? "validated" : "not validated";
    return [withdrawn ? "Withdrawn" : "Active, " + validated, withdrawn ? "red" : "green"];
  }
  if (record.intake) return ["Captured", "green"];
  return ["UNKNOWN", "muted"];
}


startViewer().catch(() => main.replaceChildren(el("div", "content-state error", "The viewer could not start.")));
