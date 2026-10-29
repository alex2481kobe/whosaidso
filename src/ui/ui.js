// DOM and presentation helpers ported from v0.2.1 internal/ui/ui.js.
import { icon } from "./ui-icons.js";
import { startViewer } from "./ui-shell.js";
export { icon };
export const main = document.getElementById("main");
export const state = { projects: [], project: "", renders: 0, screens: [] };
export const project = () => state.project;
if (location.search) history.replaceState(null, "", "/" + location.hash);
export const apiURL = (path, params = {}) => path + "?" + new URLSearchParams({project:project(), ...params});
export async function api(path, params = {}) {
  const response = await fetch(apiURL(path, params), {credentials:"same-origin"});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw Object.assign(new Error(body.error?.message || `${response.status} ${response.statusText}`), {status:response.status});
  return body;
}
export function el(tag, className = "", ...children) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children.flat()) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : String(child));
  }
  return node;
}
export const mono = text => el("span", "mono", text);
export function link(id, text = id, className = "link", kind = "goal") {
  const a = el("a", className, text); a.href = `#/record/${encodeURIComponent(kind)}/${encodeURIComponent(id)}`; return a;
}
export function copyButton(label, text) {
  const words = el("span", "", label), button = el("button", "button", icon("copy"), words);
  button.type = "button"; button.title = text;
  button.addEventListener("click", async e => {
    e.stopPropagation();
    try { await navigator.clipboard.writeText(text); words.textContent = "Copied"; button.classList.add("copied"); }
    catch { words.textContent = "Copy failed"; }
    setTimeout(() => { words.textContent = label; button.classList.remove("copied"); }, 1400);
  });
  return button;
}
export const facts = (pairs, className = "facts") => el("dl", className, pairs.filter(([,v]) => v !== null && v !== undefined && v !== "")
  .map(([k,v]) => el("div", "", el("dt", "", k), el("dd", "", v))));
export function item(title, value, detail) {
  const head = el("button", "item-head", icon("chevron"), el("span", "item-title", title), el("span", "item-value", value));
  head.type = "button"; head.setAttribute("aria-expanded", "false");
  const body = el("div", "item-body", detail); body.hidden = true;
  const row = el("li", "item", head, body);
  head.addEventListener("click", () => {
    body.hidden = !body.hidden; row.classList.toggle("open", !body.hidden); head.setAttribute("aria-expanded", String(!body.hidden));
  });
  return row;
}
export const tone = s => ({done:"green",active:"blue",landed:"amber",failing:"red",stale:"purple",open:"slate",idle:"slate"}[s] || "slate");
export const titleCase = s => String(s ?? "").replace(/^./, c => c.toUpperCase());
export const chip = s => el("span", "state-chip tone-tint " + tone(s), titleCase(s));
export const stamp = nanos => nanos == null ? "UNKNOWN" : new Date(Number(nanos) / 1000000).toLocaleString(undefined, {timeZone:"UTC",timeZoneName:"short"});
export function branchWork(branches) {
  return el("div", "branch-work", branches.map(b => {
    const hours = Math.max(0, Math.floor((Date.now() / 1000 - b.time) / 3600));
    const age = hours < 24 ? `${hours}h ago` : `${Math.floor(hours / 24)}d ago`;
    return el("div", "branch-entry", mono(b.name), el("span", "muted", `${b.author} · ${age}`),
      b.worktree ? el("span", "mono muted branch-path", b.worktree) : null);
  }));
}
export function searchBox(label, onChange, value = "") {
  const input = el("input", "search-input"); Object.assign(input, {type:"search", placeholder:label, value}); input.setAttribute("aria-label", label);
  input.addEventListener("input", () => onChange(input.value.trim().toLowerCase()));
  return el("label", "search", icon("search"), input);
}
// Each screen polls independently; replies from a previous project are discarded.
export async function live(path, draw, params = {}) {
  const shown = project(), route = location.hash, node = el("div", "live-content");
  let stamp = "", checking = false;
  const read = async () => {
    if (checking) return; checking = true;
    try {
      const data = await api(path, {...params, project:shown});
      if (shown !== project() || route !== location.hash) return;
      document.getElementById("offline").hidden = true;
      const next = JSON.stringify(data);
      if (next !== stamp) {
        draw(node, data); stamp = next;
        const heading = node.querySelector("h1");
        if (node.isConnected && heading) {
          const title = main.querySelector(".page-header")?.querySelector("h1");
          if (title) title.textContent = heading.textContent;
          heading.remove();
        }
      }
    } catch (e) {
      document.getElementById("offline").hidden = !!e.status;
      if (!stamp) throw e;
    } finally { checking = false; }
  };
  await read();
  const timer = setInterval(() => {
    if (!node.isConnected || shown !== project() || route !== location.hash) { clearInterval(timer); return; }
    read().catch(() => {});
  }, 3000);
  return {node, refresh:read};
}
startViewer().catch(e => main.replaceChildren(el("div", "content-state error", e.message)));
