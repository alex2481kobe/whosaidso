// Shared helpers; the entry point starts the viewer once they are defined.
import { icon } from "./ui-icons.js";
import { startViewer } from "./ui-shell.js";
export { icon };
export const main = document.getElementById("main");
export const state = { projects: [], project: "", renders: 0 };
if (location.search) history.replaceState(null, "", "/" + location.hash);
export const apiURL = (path, params = {}) => path + "?" + new URLSearchParams({project:state.project, ...params});
export async function api(path, params = {}) {
  const response = await fetch(apiURL(path, params), {credentials:"same-origin"});
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw Object.assign(new Error(body.error?.message || `${response.status} ${response.statusText}`), {status:response.status});
  return body;
}
export function el(tag, className = "", ...children) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  for (const child of children.flat(Infinity)) {
    if (child === null || child === undefined || child === false) continue;
    node.append(child instanceof Node ? child : String(child));
  }
  return node;
}
export const mono = text => el("span", "mono", text);
export const titleCase = s => String(s ?? "").replace(/^./, c => c.toUpperCase());
export const chip = s => el("span", "chip s-" + s, titleCase(s));
export const stamp = nanos => new Date(Number(nanos) / 1e6).toLocaleString(undefined, {month:"short", day:"numeric", hour:"numeric", minute:"2-digit"});
export function ago(seconds) {
  const s = Math.max(0, Date.now() / 1000 - seconds);
  return s < 3600 ? `${Math.floor(s / 60)}m ago` : s < 86400 ? `${Math.floor(s / 3600)}h ago` : `${Math.floor(s / 86400)}d ago`;
}
export function searchBox(label, onChange, value = "") {
  const input = el("input", "search-input"); Object.assign(input, {type:"search", placeholder:label, value}); input.setAttribute("aria-label", label);
  input.addEventListener("input", () => onChange(input.value.trim().toLowerCase()));
  return el("label", "search", icon("search"), input);
}
startViewer().catch(e => main.replaceChildren(el("div", "content-state error", e.message)));
