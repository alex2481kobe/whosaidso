// The viewer's core: API calls, the header (project switcher and theme),
// hash routing, the live watermark poll, and the small helpers every screen
// shares. Each screen lives in its own ui-*.js module. Everything shown is
// read from the views' own JSON; nothing here writes anywhere.
import { renderHome } from "./ui-home.js";
import { renderRecords } from "./ui-records.js";
import { renderDetail } from "./ui-detail.js";
import { renderHistory } from "./ui-history.js";

const token = new URLSearchParams(location.search).get("token") || "";
const main = document.getElementById("main");
const state = { projects: [], project: "", mark: "", cache: new Map() };

// ---- data
export async function api(path, params = {}) {
  const query = new URLSearchParams(params).toString();
  const response = await fetch(path + (query ? "?" + query : ""), { headers: { "X-WhoSaidSo-Token": token } });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(body.error?.message || `${response.status} ${response.statusText}`);
  return body;
}

// view answers one view for the selected project, once per watermark.
export function view(name, params = {}) {
  const key = JSON.stringify([state.project, state.mark, name, params]);
  if (!state.cache.has(key)) state.cache.set(key, api("/api/view", { project: state.project, view: name, ...params }).catch((e) => { state.cache.delete(key); throw e; }));
  return state.cache.get(key);
}

// times maps each ledger event to the time WhoSaidSo captured its packet, as
// the bundle's review.admit records it; a review's own row takes the time of
// the packets it reviewed. Unknown stays unknown.
export async function times() {
  const history = await view("history");
  const captured = new Map();
  for (const row of history.events) {
    if (row.event.type === "review.admit") captured.set(row.origin.sequence, row.event.data.captured_at || {});
  }
  const at = new Map();
  for (const row of history.events) {
    const stamps = captured.get(row.origin.sequence) || {};
    const packet = row.author?.packet || Object.keys(stamps)[0];
    const stamp = stamps[packet];
    at.set(`${row.origin.sequence}:${row.origin.event_index}`, stamp?.state === "known" ? new Date(stamp.value) : null);
  }
  return (origin) => (origin ? at.get(`${origin.sequence}:${origin.event_index}`) || null : null);
}

// titles names every current record and admitted source by its label.
export async function titles() {
  const show = await view("show");
  const names = new Map(show.records.map((r) => [r.ref.record_id, r.label]));
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
export const mono = (text, className = "") => el("span", "mono " + className, text);
export function link(id, text = id, className = "link") {
  const a = el("a", className, text);
  a.href = "#/record/" + encodeURIComponent(id);
  return a;
}

// icon draws one line icon: the six Home cards' and the disclosure chevrons.
const paths = {
  progress: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2",
  back: "M9 10l-5 5 5 5M4 15h11a5 5 0 0 0 0-10h-3",
  blocked: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM5.6 5.6l12.8 12.8",
  ready: "M5 21V4M5 4h12l-2.5 4 2.5 4H5",
  decision: "M12 21V3M6 5h10l3 3-3 3H6zM18 13H8l-3 3 3 3h10z",
  proposal: "M3 13h5l1.5 3h5l1.5-3h5M5.5 5h13L21 13v6H3v-6z",
  chevron: "M9 6l6 6-6 6",
};
export function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("icon");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", paths[name]);
  svg.append(path);
  return svg;
}
export function copyButton(label, text) {
  const button = el("button", "button", label);
  button.type = "button";
  button.addEventListener("click", async (e) => {
    e.stopPropagation();
    try { await navigator.clipboard.writeText(text); button.textContent = "Copied"; }
    catch { button.textContent = "Copy failed"; }
    setTimeout(() => { button.textContent = label; }, 1400);
  });
  return button;
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
  return el("section", "panel", el("h2", "", el("span", "", title), el("span", "count", String(items.length))),
    el("div", "panel-body", items.length ? el("ul", "items", items) : el("p", "empty", "Empty")));
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
export const titleCase = (s) => String(s).toLowerCase().replace(/(^|[\s-])\S/g, (c) => c.toUpperCase());

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
export const kindOf = (record) => (record.intake ? "Source" : titleCase(record.fact?.kind || "record"));

// ---- header: theme and projects
const toggle = document.getElementById("theme-toggle");
function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  toggle.setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
  toggle.setAttribute("aria-label", `Switch to ${theme === "dark" ? "light" : "dark"} mode`);
  try { localStorage.setItem("whosaidso-theme", theme); } catch {}
}
function initTheme() {
  let saved = null;
  try { saved = localStorage.getItem("whosaidso-theme"); } catch {}
  toggle.addEventListener("click", () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark"));
  setTheme(saved || (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"));
}

function renderMenu() {
  document.getElementById("project-name").textContent = state.project || "No project";
  const menu = document.getElementById("project-menu");
  menu.replaceChildren(...state.projects.map((p) => {
    const row = el("button", "menu-row", p.id);
    row.type = "button";
    row.setAttribute("role", "option");
    row.setAttribute("aria-selected", String(p.id === state.project));
    row.disabled = !p.available;
    row.addEventListener("click", () => { closeMenu(); select(p.id); });
    return row;
  }));
}
function closeMenu() {
  document.getElementById("project-menu").hidden = true;
  document.getElementById("project-button").setAttribute("aria-expanded", "false");
}
function initMenu() {
  const button = document.getElementById("project-button");
  button.addEventListener("click", (e) => {
    e.stopPropagation();
    const menu = document.getElementById("project-menu");
    menu.hidden = !menu.hidden;
    button.setAttribute("aria-expanded", String(!menu.hidden));
    if (!menu.hidden) api("/api/projects").then((list) => { state.projects = list.projects; renderMenu(); }).catch(() => {});
  });
  document.addEventListener("click", closeMenu);
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") closeMenu(); });
}
function select(id) {
  state.project = id;
  state.mark = "";
  state.cache.clear();
  try { sessionStorage.setItem("whosaidso-project", id); } catch {}
  location.hash = "#/";
  route();
}

// ---- routing and the live poll
async function route() {
  const [path, rest = ""] = location.hash.replace(/^#/, "").split("?");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  const screen = parts[0] || "home";
  for (const a of document.querySelectorAll("[data-nav]")) {
    a.setAttribute("aria-selected", String(a.dataset.nav === (screen === "record" ? "records" : screen)));
  }
  renderMenu();
  if (!state.project) {
    main.replaceChildren(el("p", "notice", "No WhoSaidSo project is registered on this machine."));
    return;
  }
  try {
    let node;
    if (screen === "records") node = await renderRecords(new URLSearchParams(rest).get("tab") || "all");
    else if (screen === "record") node = await renderDetail(parts[1]);
    else if (screen === "history") node = await renderHistory();
    else node = await renderHome(await view("todo"));
    main.replaceChildren(node);
  } catch (error) {
    main.replaceChildren(el("p", "notice", "Could not read this project: " + error.message));
  }
}

const markOf = (w) => (w ? `${w.sequence}:${w.head?.command_id || ""}` : "");

// poll reads the selected project's watermark through its todo view and
// re-renders only when it moved. A stopped viewer says so in the header.
async function poll() {
  const offline = document.getElementById("offline");
  try {
    if (!state.project) return;
    const mark = markOf((await api("/api/view", { project: state.project, view: "todo" })).watermark);
    offline.hidden = true;
    if (mark !== state.mark) {
      state.mark = mark;
      state.cache.clear();
      route();
    }
  } catch {
    offline.hidden = false;
  }
}

// boot opens the project this tab last showed, else the one the command ran
// in, else the first available one. The dropdown is the only switch.
async function boot() {
  initTheme();
  initMenu();
  const list = await api("/api/projects");
  state.projects = list.projects;
  let remembered = "";
  try { remembered = sessionStorage.getItem("whosaidso-project") || ""; } catch {}
  const usable = (id) => state.projects.some((p) => p.id === id && p.available);
  state.project = usable(remembered) ? remembered : usable(list.current) ? list.current : state.projects.find((p) => p.available)?.id || "";
  state.mark = markOf(state.projects.find((p) => p.id === state.project)?.watermark);
  window.addEventListener("hashchange", route);
  await route();
  setInterval(poll, 3000);
}

boot().catch((error) => main.replaceChildren(el("p", "notice", "The viewer could not start: " + error.message)));
