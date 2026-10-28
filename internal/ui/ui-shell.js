// Viewer chrome and routing belong here; shared DOM/data helpers are ui.js's.
// Each screen owns its bounded refresh, and late routes cannot replace a newer one.
import { main, state, el, project, api, icon } from "./ui.js";
import { place } from "./ui-parts.js";
import { renderBrowser } from "./ui-browser.js";
import { renderDetail } from "./ui-detail.js";
import { renderGallery } from "./ui-gallery.js";
// ---- header: theme and projects
const toggle = document.getElementById("theme-toggle");
function setTheme(theme) {
  document.documentElement.dataset.theme = theme;
  toggle.replaceChildren(icon(theme === "dark" ? "sun" : "moon"));
  toggle.setAttribute("aria-pressed", theme === "dark" ? "true" : "false");
  toggle.setAttribute("aria-label", `Switch to ${theme === "dark" ? "light" : "dark"} mode`);
  try { localStorage.setItem("whosaidso-theme", theme); } catch {}
}
function initTheme() {
  let saved = null;
  try { saved = localStorage.getItem("whosaidso-theme"); } catch {}
  toggle.addEventListener("click", () => setTheme(document.documentElement.dataset.theme === "dark" ? "light" : "dark"));
  setTheme(saved === "light" || saved === "dark" ? saved : (matchMedia("(prefers-color-scheme: dark)").matches ? "dark" : "light"));
}

function renderMenu() {
  document.getElementById("project-name").textContent = state.project || "No workspace";
  document.getElementById("project-button").setAttribute("aria-label", "Choose workspace, " + (state.project || "none selected"));
  const menu = document.getElementById("project-menu");
  const focused = menu.contains(document.activeElement) ? document.activeElement.dataset.project : null;
  menu.replaceChildren(...state.projects.map((p) => {
    const row = el("button", "menu-row", el("span", "workspace-name", p.id),
      p.id === state.project ? icon("check") : !p.available ? el("span", "menu-extra", "Unavailable") : null);
    row.type = "button";
    row.dataset.project = p.id;
    row.tabIndex = -1;
    row.setAttribute("role", "option");
    row.setAttribute("aria-selected", String(p.id === state.project));
    row.disabled = !p.available;
    row.addEventListener("click", () => { closeMenu(true); select(p.id); });
    return row;
  }));
  if (focused && !menu.hidden) {
    const available = [...menu.children].filter((row) => !row.disabled);
    (available.find((row) => row.dataset.project === focused) || available.find((row) => row.getAttribute("aria-selected") === "true") || available[0] || document.getElementById("project-button")).focus();
  }
}
function closeMenu(restoreFocus = false) {
  document.getElementById("project-menu").hidden = true;
  const button = document.getElementById("project-button");
  button.setAttribute("aria-expanded", "false");
  if (restoreFocus) button.focus();
}
function initMenu() {
  const button = document.getElementById("project-button");
  const menu = document.getElementById("project-menu");
  const options = () => [...menu.querySelectorAll("button:not(:disabled)")];
  const focusOption = (last = false) => {
    const rows = options();
    (last ? rows.at(-1) : rows.find((row) => row.getAttribute("aria-selected") === "true") || rows[0])?.focus();
  };
  const open = (last = false) => {
    menu.hidden = false;
    button.setAttribute("aria-expanded", "true");
    place(menu, button);
    focusOption(last);
    api("/api/projects", {summary:"0"}).then((list) => {
      state.projects = list.projects;
      renderMenu();
      if (!menu.hidden) { place(menu, button); if (document.activeElement === button) focusOption(last); }
    }).catch(() => {});
  };
  button.addEventListener("click", (e) => { e.stopPropagation(); if (menu.hidden) open(); else closeMenu(true); });
  button.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown" || e.key === "ArrowUp") { e.preventDefault(); open(e.key === "ArrowUp"); }
  });
  menu.addEventListener("keydown", (e) => {
    const rows = options(), index = rows.indexOf(document.activeElement);
    let next;
    if (e.key === "ArrowDown") next = rows[(index + 1) % rows.length];
    if (e.key === "ArrowUp") next = rows[(index - 1 + rows.length) % rows.length];
    if (e.key === "Home") next = rows[0];
    if (e.key === "End") next = rows.at(-1);
    if (e.key.length === 1 && e.key !== " " && !e.ctrlKey && !e.metaKey) {
      next = rows.slice(index + 1).concat(rows.slice(0, index + 1)).find((row) => row.dataset.project.toLowerCase().startsWith(e.key.toLowerCase()));
    }
    if (next) { e.preventDefault(); next.focus(); }
    if (e.key === "Tab") closeMenu(true);
  });
  document.addEventListener("click", () => closeMenu());
  document.addEventListener("keydown", (e) => { if (e.key === "Escape" && !menu.hidden) { e.preventDefault(); closeMenu(true); } });
}
async function select(id) {
  state.switches++;
  state.renders++;
  state.project = id;
  state.mark = "";
  state.cache.clear();
  try { sessionStorage.setItem("whosaidso-project", id); } catch {}
  location.hash = "#/gallery";
  route();
}

// ---- routing and the live poll
let lastRoute = "", lastProject = "", lastScreen = "";
function routeTarget() {
  const [path, rest = ""] = location.hash.replace(/^#/, "").split("?");
  const parts = path.split("/").filter(Boolean).map(decodeURIComponent);
  return {screen:parts[0] || "gallery", parts, rest};
}
const scrollPositions = new Map();
// Save while the current screen is active, before native hash navigation can
// reset scroll. Delay restoration until the replacement layout has settled.
if ("scrollRestoration" in history) history.scrollRestoration = "manual";
const rememberScroll = () => {
  if (lastRoute && lastRoute === project() + location.hash)
    scrollPositions.set(lastRoute, { page: main.scrollTop, window: window.scrollY });
};
window.addEventListener("scroll", rememberScroll, { passive: true });

document.addEventListener("click", (event) => {
  if (event.target.closest("a[href^='#/']")) rememberScroll();
}, true);
async function route() {
  const routeKey = project() + location.hash;
  if (lastRoute === routeKey) rememberScroll();
  const restore = routeKey !== lastRoute || project() !== lastProject;
  lastRoute = routeKey; lastProject = project();
  const mine = ++state.renders;
  const show = (node) => {
    if (mine !== state.renders) return;
    const saved = scrollPositions.get(routeKey);
    main.replaceChildren(node);
    if (restore) {
      const apply = () => { if (mine === state.renders) { main.scrollTop = saved?.page || 0; window.scrollTo(0, saved?.window || 0); } };
      apply();
      requestAnimationFrame(() => { apply(); requestAnimationFrame(apply); });
    }
  };
  const {screen, parts, rest} = routeTarget();
  // Gallery has its own fresh page poll. Views cached before visiting it
  // must be read again when the reader returns to a record screen.
  if (lastScreen === "gallery" && screen !== "gallery") state.cache.clear();
  lastScreen = screen;
  for (const a of document.querySelectorAll("[data-nav]")) {
    if (a.dataset.nav === (screen === "record" ? "records" : screen)) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
  renderMenu();
  if (!state.project) {
    show(el("p", "notice", "No WhoSaidSo project is registered on this machine."));
    return;
  }
  if (restore) {
    const loading = el("p", "notice", "Loading " + ({gallery:"gallery", records:"records", tasks:"tasks", history:"activity", record:"record", home:"overview"}[screen] || "overview") + "…");
    loading.setAttribute("role", "status"); show(loading);
  }
  try {
    let node;
    if (screen === "tasks") node = await renderBrowser("records", new URLSearchParams("kind=TASK"));
    else if (screen === "records") node = await renderBrowser("records", new URLSearchParams(rest));
    else if (screen === "record") {
      const status = await api("/api/screen", {project:project(), screen:"status"});
      if (mine !== state.renders) return;
      state.mark = JSON.stringify(status.watermark);
      node = await renderDetail(parts[1]);
    }
    else if (screen === "history") node = await renderBrowser("history");
    else if (screen === "gallery") node = await renderGallery();
    else node = await renderBrowser("home");
    show(node);
  } catch (error) {
    show(el("p", "notice", "Could not read this project: " + error.message));
  }
}

// boot opens the project this tab last showed, else the one the command ran
// in, else the first available one. The dropdown is the only switch.
let checking = false;
async function checkRecord() {
  if (checking || routeTarget().screen !== "record" || document.visibilityState === "hidden") return;
  const asked = state.switches, hash = location.hash;
  checking = true;
  try {
    const status = await api("/api/screen", {project:project(), screen:"status"});
    if (asked !== state.switches || hash !== location.hash) return;
    document.getElementById("offline").hidden = true;
    const mark = JSON.stringify(status.watermark);
    if (mark !== state.mark) { state.cache.clear(); state.mark = mark; route(); }
  } catch { if (asked === state.switches && hash === location.hash) document.getElementById("offline").hidden = false; }
  finally { checking = false; }
}
export async function startViewer() {
 main.addEventListener("scroll",rememberScroll,{passive:true});
  initTheme();
  initMenu();
  const list = await api("/api/projects", {summary:"0"});
  state.projects = list.projects;
  let remembered = "";
  try { remembered = sessionStorage.getItem("whosaidso-project") || ""; } catch {}
  const usable = (id) => state.projects.some((p) => p.id === id && p.available);
  state.project = usable(remembered) ? remembered : usable(list.current) ? list.current : state.projects.find((p) => p.available)?.id || "";

  window.addEventListener("hashchange", route);
  await route();
 setInterval(checkRecord,3000);
 document.addEventListener("visibilitychange",checkRecord);
}
