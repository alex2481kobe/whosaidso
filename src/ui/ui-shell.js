// Project menus, theme and routing ported from v0.2.1 internal/ui/ui-shell.js.
import { main, state, el, api, icon } from "./ui.js";
import { pageFrame, message } from "./ui-components.js";
import { place } from "./ui-parts.js";
import { renderOverview } from "./ui-overview.js";
import { renderBrowser } from "./ui-browser.js";
import { renderDetail } from "./ui-detail.js";
import { renderGallery } from "./ui-gallery.js";
import { renderActivity } from "./ui-activity.js";
const toggle = document.getElementById("theme-toggle");
const names = {gallery:"Gallery", overview:"Overview", goals:"Goals", records:"Records", record:"Record", activity:"Activity"};
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
  state.project = id; state.renders++;
  try { sessionStorage.setItem("whosaidso-project", id); } catch {}
  state.screens = [];
  const selected = state.project;
  let screens;
  try { screens = (await api("/api/overview")).screens; }
  catch { screens = ["gallery","overview","goals","records","activity"]; }
  if (selected !== state.project) return;
  state.screens = screens;
  for (const a of document.querySelectorAll("[data-nav]")) a.hidden = !state.screens.includes(a.dataset.nav);
  location.hash = "#/" + (state.screens.includes("gallery") ? "gallery" : state.screens.includes("overview") ? "overview" : "records");
  route();
}
const positions = new Map();
let last = "";
if ("scrollRestoration" in history) history.scrollRestoration = "manual";
async function route() {
  const mine = ++state.renders, key = state.project + location.hash;
  const [path] = location.hash.replace(/^#/, "").split("?");
  let parts;
  try { parts = path.split("/").filter(Boolean).map(decodeURIComponent); } catch { parts = []; }
  const screen = parts[0] || (state.screens.includes("gallery") ? "gallery" : "overview");
  for (const a of document.querySelectorAll("[data-nav]")) {
    if (a.dataset.nav === (screen === "record" ? "records" : screen)) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  renderMenu();
  const show = node => {
    if (mine !== state.renders) return;
    main.replaceChildren(pageFrame(node, names[screen] || "Overview"));
    last = key; main.scrollTop = positions.get(key) || 0;
  };
  if (!state.project) { show(message("No workspace is available.")); return; }
  show(message("Loading " + (names[screen] || "workspace").toLowerCase() + "…", "loading"));
  try {
    let node;
    if (screen === "goals" || screen === "records") node = await renderBrowser(screen);
    else if (screen === "record") node = await renderDetail(parts[1], parts[2]);
    else if (screen === "gallery") node = await renderGallery();
    else if (screen === "activity") node = await renderActivity();
    else node = await renderOverview();
    show(node);
  } catch (error) {
    const retry = el("button", "button", icon("refresh"), "Refresh"); retry.addEventListener("click", route);
    show(el("div", "screen", message(error.message, "error"), retry));
  }
}
export async function startViewer() {
  main.addEventListener("scroll", () => { if (last) positions.set(last, main.scrollTop); }, {passive:true});
  initTheme(); initMenu();
  for (const a of document.querySelectorAll("[data-nav]")) a.hidden = true;
  const list = await api("/api/projects"); state.projects = list.projects;
  let remembered;
  try { remembered = sessionStorage.getItem("whosaidso-project"); } catch {}
  const usable = id => state.projects.some(p => p.id === id && p.available);
  state.project = usable(remembered) ? remembered : usable(list.current) ? list.current : state.projects.find(p => p.available)?.id || "";
  window.addEventListener("hashchange", route);
  if (state.project) {
    const hash = location.hash;
    try { state.screens = (await api("/api/overview")).screens; } catch { state.screens = ["gallery","overview","goals","records","activity"]; }
    for (const a of document.querySelectorAll("[data-nav]")) a.hidden = !state.screens.includes(a.dataset.nav);
    if (!hash) { location.hash = "#/" + (state.screens.includes("gallery") ? "gallery" : state.screens.includes("overview") ? "overview" : "records"); return; }
  }
  await route();
}
