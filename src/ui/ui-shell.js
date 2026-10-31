// Project menu, theme, routing between the two screens, and the change poll that keeps them current.
import { main, state, el, api, icon } from "./ui.js";
import { message } from "./ui-components.js";
import { place } from "./ui-parts.js";
import { goalsScreen } from "./ui-goals.js";
import { flowsScreen } from "./ui-flows.js";
import { galleryScreen } from "./ui-gallery.js";
const toggle = document.getElementById("theme-toggle");
function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  toggle.replaceChildren(icon(theme === "dark" ? "sun" : "moon"));
  toggle.setAttribute("aria-pressed", String(theme === "dark"));
  toggle.setAttribute("aria-label", `Switch to ${theme === "dark" ? "light" : "dark"} mode`);
  try { localStorage.setItem("whosaidso-theme", theme); } catch {}
}
function initTheme() {
  let saved;
  try { saved = localStorage.getItem("whosaidso-theme"); } catch {}
  toggle.addEventListener("click", () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark"));
  setTheme(["light","dark"].includes(saved) ? saved : matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light");
}
function renderMenu() {
  const chosen = state.projects.find(p => p.id === state.project);
  document.getElementById("project-name").textContent = chosen?.name || "No workspace";
  document.getElementById("project-button").setAttribute("aria-label", "Choose workspace, " + (chosen?.name || "none selected"));
  const menu = document.getElementById("project-menu");
  const focused = menu.contains(document.activeElement) ? document.activeElement.dataset.project : null;
  menu.replaceChildren(...state.projects.map(p => {
    const words = el("span", "workspace-name", p.name, !p.available ? el("small", "workspace-reason", p.reason) : null);
    const row = el("button", "menu-row", words, p.id === state.project ? icon("check") : null);
    row.type = "button"; row.dataset.project = p.id; row.tabIndex = -1;
    row.title = p.id; row.setAttribute("role", "option"); row.setAttribute("aria-selected", String(p.id === state.project));
    row.disabled = !p.available;
    row.addEventListener("click", () => { closeMenu(true); select(p.id); });
    return row;
  }));
  if (focused && !menu.hidden) {
    const rows = [...menu.children].filter(r => !r.disabled);
    (rows.find(r => r.dataset.project === focused) || rows[0] || document.getElementById("project-button")).focus();
  }
}
function closeMenu(restore = false) {
  document.getElementById("project-menu").hidden = true;
  const button = document.getElementById("project-button"); button.setAttribute("aria-expanded", "false");
  if (restore) button.focus();
}
function initMenu() {
  const button = document.getElementById("project-button"), menu = document.getElementById("project-menu");
  const options = () => [...menu.querySelectorAll("button:not(:disabled)")];
  const focus = (last = false) => {
    const rows = options(); (last ? rows.at(-1) : rows.find(r => r.getAttribute("aria-selected") === "true") || rows[0])?.focus();
  };
  const open = (last = false) => {
    menu.hidden = false; button.setAttribute("aria-expanded", "true"); place(menu, button); focus(last);
    api("/api/projects").then(list => {
      state.projects = list.projects; renderMenu();
      if (!menu.hidden) { place(menu, button); if (document.activeElement === button) focus(last); }
    }).catch(() => {});
  };
  button.addEventListener("click", e => { e.stopPropagation(); if (menu.hidden) open(); else closeMenu(true); });
  button.addEventListener("keydown", e => {
    if (["ArrowDown","ArrowUp"].includes(e.key)) { e.preventDefault(); open(e.key === "ArrowUp"); }
  });
  menu.addEventListener("keydown", e => {
    const rows = options(), index = rows.indexOf(document.activeElement); let next;
    if (e.key === "ArrowDown") next = rows[(index + 1) % rows.length];
    if (e.key === "ArrowUp") next = rows[(index - 1 + rows.length) % rows.length];
    if (e.key === "Home") next = rows[0];
    if (e.key === "End") next = rows.at(-1);
    if (e.key.length === 1 && e.key !== " " && !e.ctrlKey && !e.metaKey) {
      next = rows.slice(index + 1).concat(rows.slice(0, index + 1)).find(r => r.textContent.toLowerCase().startsWith(e.key.toLowerCase()));
    }
    if (next) { e.preventDefault(); next.focus(); }
    if (e.key === "Tab") closeMenu(true);
  });
  document.addEventListener("click", () => closeMenu());
  document.addEventListener("keydown", e => { if (e.key === "Escape" && !menu.hidden) { e.preventDefault(); closeMenu(true); } });
}
async function select(id) {
  state.project = id;
  try { sessionStorage.setItem("whosaidso-project", id); } catch {}
  await route();
}
const positions = new Map();
let current = null, seen = null, last = "", checking = false;
if ("scrollRestoration" in history) history.scrollRestoration = "manual";
async function route() {
  if (last) positions.set(last, main.scrollTop);
  last = "";
  const mine = ++state.renders;
  const requested = location.hash.replace(/^#\//, "").split("?")[0];
  const screen = ["gallery", "flows"].includes(requested) ? requested : "goals";
  for (const a of document.querySelectorAll("[data-nav]")) {
    if (a.dataset.nav === screen) a.setAttribute("aria-current", "page"); else a.removeAttribute("aria-current");
  }
  renderMenu();
  current?.dispose?.();
  current = null;
  if (!state.project) { main.replaceChildren(message("No workspace")); return; }
  const shown = screen === "gallery" ? galleryScreen() : screen === "flows" ? flowsScreen() : goalsScreen(), key = state.project + screen + (screen === "flows" ? location.hash : "");
  const branch = screen === "flows" && new URLSearchParams(location.hash.split("?")[1] || "").get("branch");
  if (branch) {
    const load = shown.load;
    shown.load = async () => {
      const version = await load(), {age} = await api("/api/branch", {name:branch});
      const label = el("span", "muted branch-age", age); label.setAttribute("aria-label", "Branch age " + age);
      shown.node.querySelector(".branch-age")?.remove();
      shown.node.querySelector(".toolbar").append(label);
      return version;
    };
  }
  main.replaceChildren(shown.node);
  try { seen = await shown.load(); }
  catch (error) { if (mine === state.renders) main.replaceChildren(message(error.message, "error")); return; }
  if (mine !== state.renders) return;
  current = shown; last = key; main.scrollTop = positions.get(key) || 0;
}
// Poll the server for its cheap change token; load a screen again only when it moved.
async function poll() {
  if (document.hidden || !current || checking) return;
  checking = true;
  const mine = state.renders, shown = current;
  try {
    const { version } = await api("/api/version");
    document.getElementById("offline").hidden = true;
    if (version !== seen) {
      const next = await shown.load();
      if (mine === state.renders) seen = next;
    }
  } catch (e) { document.getElementById("offline").hidden = !!e.status; }
  finally { checking = false; }
}
export async function startViewer() {
  main.addEventListener("scroll", () => { if (last) positions.set(last, main.scrollTop); }, {passive:true});
  initTheme(); initMenu();
  const list = await api("/api/projects"); state.projects = list.projects;
  let remembered;
  try { remembered = sessionStorage.getItem("whosaidso-project"); } catch {}
  const usable = id => state.projects.some(p => p.id === id && p.available);
  state.project = usable(remembered) ? remembered : usable(list.current) ? list.current : state.projects.find(p => p.available)?.id || "";
  window.addEventListener("hashchange", route);
  setInterval(poll, 1000);
  document.addEventListener("visibilitychange", poll);
  await route();
}
