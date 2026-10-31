import { el, api, chip, mono } from "./ui.js";
import { dropdown } from "./ui-parts.js";
import { message } from "./ui-components.js";

const route = (id = "", parent = "", branch = "", offset = 0) => "#/flows?" + new URLSearchParams({id, parent, branch, offset});
export function flowsScreen() {
  const query = new URLSearchParams(location.hash.split("?")[1] || "");
  const id = query.get("id") || "", parent = query.get("parent") || "", branch = query.get("branch") || "";
  const offset = query.get("offset") || "0";
  const toolbar = el("div", "toolbar"), list = el("div", "milestone"), status = el("span", "totals");
  const node = el("div", "screen", toolbar, list);
  const link = (label, id, parent = "", page = 0) => Object.assign(el("a", "button", label), {href:route(id, parent, branch, page)});
  const row = n => {
    const label = el("span", "goal-title", n.name || n.id), data = [chip(n.state), label, el("span", "muted", n.type), n.evidence && el("span", "muted", n.evidence)];
    if (n.path) data.push(mono(n.path + (n.line ? ":" + n.line : "")));
    if (n.children) return Object.assign(el("a", "menu-row flow-row", data), {href:route(id, n.id, branch)});
    return el("div", "menu-row flow-row", data);
  };
  return {node, async load() {
    const data = await api("/api/flows", {id, parent, offset, branch});
    node.dataset.revision = data.revision || "";
    status.replaceChildren(data.state && chip(data.state), data.progress && ` ${data.progress.built}/${data.progress.total}`, data.committed_only && " · committed-only", data.freshness && data.freshness !== "fresh" && ` · ${data.freshness}`);
    toolbar.replaceChildren(link("Flows", ""), id && link(id === "$map" ? "Code" : id, id), parent && el("span", "muted", parent), status);
    if (data.branches) toolbar.append(dropdown({options:[["", "Worktree"], ...data.branches.map(b => [b,b])], value:branch, onPick:b => { location.hash = route(id,"",b); }}));
    if (!id) {
      list.replaceChildren(Object.assign(el("a", "menu-row", "Code"), {href:route("$map", "", branch)}), ...(data.flows || []).map(f => Object.assign(el("a", "menu-row flow-row", chip(f.state), el("span", "goal-title", f.id), f.progress && `${f.progress.built}/${f.progress.total}`), {href:route(f.id,"",branch)})));
    } else {
      list.replaceChildren(...(data.nodes || []).map(row));
      for (const s of data.steps || []) list.append(el("div", "menu-row flow-row", chip(s.state), mono(s.from), " → ", mono(Array.isArray(s.to) ? s.to.join(", ") : s.to), s.when && el("span", "muted", s.when), el("span", "muted", s.evidence)));
      for (const r of data.risks || []) list.append(el("details", "goal", el("summary", "menu-row", r.risk.id), el("div", "goal-detail", r.risk.note, Object.entries(r.risk.mitigation).map(([type,id]) => el("span", "muted", `${type}:${id}`)))));
      for (const p of data.problems || []) list.append(message(p, "error"));
      for (const p of data.unknown || []) list.append(el("div", "menu-row", chip("unknown"), mono(p)));
      for (const u of data.unplanned || []) list.append(el("div", "menu-row flow-row", chip(u.state), mono(u.path), mono(u.to)));
      if (data.next != null) list.append(link("Next", id, parent, data.next));
    }
    if (!id && data.next != null) list.append(link("Next", "", "", data.next));
    return data.version;
  }};
}
