// The viewer's core: API calls, the header (project switcher, watermark,
// theme), hash routing, the live watermark poll, and the small helpers every
// screen shares. Each screen lives in its own ui-*.js module. Everything
// shown is read from the views' own JSON; nothing here writes anywhere.
import { renderHome } from "./ui-home.js";
import { renderRecords } from "./ui-records.js";
import { renderDetail } from "./ui-detail.js";
import { renderHistory, renderProjects } from "./ui-history.js";

const token = new URLSearchParams(location.search).get("token") || "";
const main = document.getElementById("main");
const state = { projects: [], project: "", mark: "", cache: new Map(), lastRecord: "" };

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
export function link(id, text = id, className = "mono link") {
  const a = el("a", className, text);
  a.href = "#/record/" + encodeURIComponent(id);
  return a;
}
// art is a copy of one of index.html's static illustrations.
export const art = (id) => document.getElementById(id).content.firstElementChild.cloneNode(true);
export function icon(name) {
  const paths = { copy: "M8 8h10v12H8zM5 16V4h10", command: "M9 9h10v10H9zM5 15V5h10", back: "M19 12H5m6-6-6 6 6 6", warn: "M12 3 2 21h20L12 3zm0 7v5m0 3v.5", owed: "M4 6h16M4 12h10M4 18h7" };
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.classList.add("icon");
  const path = document.createElementNS("http://www.w3.org/2000/svg", "path");
  path.setAttribute("d", paths[name]);
  svg.append(path);
  return svg;
}
export function copyButton(label, text, glyph = "copy") {
  const button = el("button", "button", icon(glyph), el("span", "", label));
  button.type = "button";
  button.addEventListener("click", async () => {
    try { await navigator.clipboard.writeText(text); button.lastChild.textContent = "Copied"; }
    catch { button.lastChild.textContent = "Copy failed"; }
    setTimeout(() => { button.lastChild.textContent = label; }, 1400);
  });
  return button;
}

// ---- words: the ledger's values, labelled for people
export function actorName(actor) {
  if (!actor) return "UNKNOWN";
  if (actor.id) return actor.id;
  return "UNKNOWN";
}
export function actorReason(actor) {
  return actor?.unknown_reason || actor?.reason || "";
}
const monthDay = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" });
const clock = new Intl.DateTimeFormat(undefined, { hour: "2-digit", minute: "2-digit", hour12: false });
const fullDay = new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric", year: "numeric" });
export const day = (d) => (d ? fullDay.format(d) : "UNKNOWN");
export const stamp = (d) => (d ? `${fullDay.format(d)} ${clock.format(d)}` : "UNKNOWN");
export const when = (d) => (d ? `${monthDay.format(d)}, ${clock.format(d)}` : "UNKNOWN");
export function since(d) {
  if (!d) return "since UNKNOWN";
  return new Date().toDateString() === d.toDateString() ? "since " + clock.format(d) : "since " + monthDay.format(d);
}
export const truth = (t) => ({ TRUE: "TRUE", FALSE: "FALSE" }[t] || "UNKNOWN");
export const titleCase = (s) => String(s).toLowerCase().replace(/(^|[\s-])\S/g, (c) => c.toUpperCase());

// status is a record's real status, labelled, with the tone that colours it.
export function status(record) {
  if (record.task) {
    const t = record.task;
    if (t.status === "IN FLIGHT") return ["In progress", "blue"];
    if (t.status === "READY") return ["Ready", "amber"];
    if (t.status === "CLOSED") return [t.outcome === "success" ? "Closed" : "Closed · " + t.outcome, "muted"];
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
    const trust = record.instrument.trust;
    return [trust === "TRUE" ? "Trusted" : trust === "FALSE" ? "Not trusted" : "Trust UNKNOWN", trust === "TRUE" ? "green" : trust === "FALSE" ? "red" : "muted"];
  }
  if (record.intake) return ["Captured", "green"];
  return ["UNKNOWN", "muted"];
}
export const kindOf = (record) => (record.intake ? "Source" : titleCase(record.fact?.kind || "record"));

// ---- header, theme, projects
function applyTheme(dark) {
  document.documentElement.dataset.theme = dark ? "dark" : "light";
  const toggle = document.getElementById("theme-toggle");
  toggle.setAttribute("aria-checked", String(dark));
}
function initTheme() {
  let saved = null;
  try { saved = localStorage.getItem("whosaidso-theme"); } catch {}
  applyTheme(saved ? saved === "dark" : matchMedia("(prefers-color-scheme: dark)").matches);
  document.getElementById("theme-toggle").addEventListener("click", () => {
    const dark = document.documentElement.dataset.theme !== "dark";
    applyTheme(dark);
    try { localStorage.setItem("whosaidso-theme", dark ? "dark" : "light"); } catch {}
  });
}

function renderHeader(todo) {
  const entry = state.projects.find((p) => p.id === state.project);
  document.getElementById("project-name").textContent = state.project || "All projects";
  const w = todo?.watermark || entry?.watermark;
  const head = w?.head?.recorded_at ? clock.format(new Date(w.head.recorded_at)) : "UNKNOWN";
  document.getElementById("project-meta").textContent = w ? `latest #${w.sequence} · ${w.events} entries · updated ${head}` : `${state.projects.length} projects on this machine`;
  renderMenu();
}
function renderMenu() {
  const menu = document.getElementById("project-menu");
  menu.replaceChildren(...state.projects.map((p) => {
    const row = el("button", "menu-row" + (p.id === state.project ? " selected" : ""), el("span", "menu-name", p.id),
      el("span", "menu-note", p.available ? `${p.totals.in_flight} in flight · ${p.totals.blocked} blocked · ${p.totals.ready} ready` : "unavailable: " + p.reason));
    row.type = "button";
    row.disabled = !p.available;
    row.addEventListener("click", () => { closeMenu(); select(p.id); });
    return row;
  }), el("a", "menu-row menu-all", "All projects on this machine"));
  menu.lastChild.href = "#/projects";
  menu.lastChild.addEventListener("click", closeMenu);
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
  const screen = !state.project || parts[0] === "projects" ? "projects" : parts[0] || "home";
  for (const a of document.querySelectorAll("[data-nav]")) {
    a.classList.toggle("active", a.dataset.nav === (screen === "record" ? "records" : screen));
  }
  try {
    const todo = state.project ? await view("todo") : null;
    renderHeader(todo);
    let node;
    if (screen === "projects") node = renderProjects(state.projects, select);
    else if (screen === "records") node = await renderRecords(new URLSearchParams(rest).get("tab") || "all");
    else if (screen === "record") { state.lastRecord = parts[1]; node = await renderDetail(parts[1]); }
    else if (screen === "history") node = await renderHistory(parts[1] || "", state.lastRecord);
    else node = await renderHome(todo);
    main.replaceChildren(node);
  } catch (error) {
    main.replaceChildren(el("section", "notice", "Could not read this project: " + error.message));
  }
}

const markOf = (w) => (w ? `${w.sequence}:${w.head?.command_id || ""}` : "");

// poll reads the selected project's watermark through its todo view (the
// project list when none is selected) and re-renders only when it moved.
async function poll() {
  try {
    let mark;
    if (state.project) {
      mark = markOf((await api("/api/view", { project: state.project, view: "todo" })).watermark);
    } else {
      const list = await api("/api/projects");
      mark = JSON.stringify(list.projects.map((p) => [p.id, p.available, markOf(p.watermark)]));
      state.projects = list.projects;
    }
    if (mark !== state.mark) {
      state.mark = mark;
      state.cache.clear();
      route();
    }
  } catch {
    document.getElementById("project-meta").textContent = "Viewer disconnected. Run whosaidso ui again.";
  }
}

async function boot() {
  initTheme();
  initMenu();
  const list = await api("/api/projects");
  state.projects = list.projects;
  let remembered = "";
  try { remembered = sessionStorage.getItem("whosaidso-project") || ""; } catch {}
  const usable = (id) => state.projects.some((p) => p.id === id && p.available);
  state.project = usable(remembered) ? remembered : usable(list.current) ? list.current : "";
  state.mark = markOf(state.projects.find((p) => p.id === state.project)?.watermark);
  window.addEventListener("hashchange", route);
  await route();
  setInterval(poll, 3000);
}

boot().catch((error) => main.replaceChildren(el("section", "notice", "The viewer could not start: " + error.message)));
