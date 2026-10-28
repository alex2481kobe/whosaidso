// The owner overview renders server-selected actions. Record browsing and
// task navigation belong to ui-browser.js.
import { el, api, project, link } from "./ui.js";

export async function renderOverview() {
  const shown = project(), route = location.hash;
  const active = () => shown === project() && route === location.hash;
  let page = await api("/api/screen", {project:shown, screen:"home"});
  const counts = el("div", "browser-counts");
  const flying = el("section", "panel");
  const waiting = el("section", "panel");
  const said = el("p", "muted"); said.setAttribute("role", "status");
  const previous = el("button", "button", "Previous");
  const next = el("button", "button", "Next");
  previous.type = next.type = "button";
  const range = el("span", "muted");
  const root = el("div", "screen overview-screen", el("h1", "", "Overview"), counts, flying, waiting, said, el("nav", "gallery-tools", previous, range, next));
  function draw(answer) {
    page = answer;
    counts.replaceChildren(...Object.entries(page.counts).map(([status, n]) => el("span", "", `${status}: ${n}`)));
    for (const [panel, group, title] of [[flying,"in_flight","In flight"], [waiting,"awaiting_decision","Awaiting your decision"]]) {
      const rows = page.rows.filter(row => row.group === group).map(row => el("div", "row-line browser-row", link(row.id, row.title, "row-title"), el("span", "muted", row.status), el("span", "", row.actor)));
      panel.replaceChildren(el("h2", "", title), ...(rows.length ? rows : [el("p", "empty", "None on this page.")]));
    }
    previous.disabled = page.previous == null; next.disabled = page.next == null;
    range.textContent = page.total ? `${page.offset+1}–${page.offset+page.rows.length} of ${page.total} actions` : "No actions waiting";
  }
  let busy = false;
  async function refresh(offset = page.offset) {
    if (!active() || offset == null || busy) return;
    busy = true;
    try {
      const answer = await api("/api/screen", {project:shown, screen:"home", offset});
      if (active() && root.isConnected) { draw(answer); said.textContent = ""; }
    } catch (error) { if (active()) said.textContent = "Could not refresh: " + error.message; }
    finally { busy = false; }
  }
  previous.addEventListener("click", () => refresh(page.previous));
  next.addEventListener("click", () => refresh(page.next));
  draw(page);
  const timer = setInterval(() => {
    if (!active() || !root.isConnected) { clearInterval(timer); return; }
    if (document.visibilityState !== "hidden") refresh();
  }, 3000);
  return root;
}
