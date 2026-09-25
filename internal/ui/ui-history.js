// History: stat cards per event group with sparklines, the filter bar
// (search, event family, date range, event chips), the history log and, on
// the right, the event breakdown and recent activity. All filtering happens
// here over the history view's events (ui-derive.js). A log row is one line:
// when, what happened in plain words, the record it names and who proposed
// it. An opened row shows its title and proposer whole, then who proposed
// and who approved it, the review's reason and the record.
import { el, when, copyButton, openButton, facts, disclose, icon, tablePanel, project } from "./ui.js";
import { activity, perDay, within, count } from "./ui-derive.js";
import { hero, statCard, sparkline, head, more, chip, dropdown, rowMenu } from "./ui-parts.js";
import { groups, other, families, inFamily, mark } from "./ui-icons.js";

// state survives re-renders from the live poll, so a filter stays put.
const state = { search: "", family: "all", range: "all", type: "all", sort: "newest" };
const ranges = [["7", "Last 7 days"], ["30", "Last 30 days"], ["all", "All time"]];
const sorts = [["newest", "Newest first"], ["oldest", "Oldest first"]];
const inRange = (r) => state.range === "all" || within(r.time, Number(state.range));
// a group's key is its event type, so one comparison serves groups and the rest
const inType = (r) => state.type === "all" || (state.type === "other" ? r.group === other : r.type === state.type);

function logRow(r) {
  const detail = el("td", "", facts([["Proposed by", r.proposer], [r.approverLabel, r.approver], ["Reason", r.reason],
    ["Record", r.target ? el("span", "id-line", openButton(r.target), copyButton("Copy id", r.target), el("span", "mono muted", r.target)) : ""]], "facts wide"));
  detail.colSpan = 5;
  const cell = (className, content, text) => { const td = el("td", className, content); td.title = text ?? content; return td; };
  const menu = el("td", "row-actions", r.target ? rowMenu("Event actions", [["Open record", () => { location.hash = "#/record/" + encodeURIComponent(r.target); }],
    ["Copy id", () => navigator.clipboard.writeText(r.target).catch(() => {})]]) : null);
  menu.addEventListener("click", (e) => e.stopPropagation());
  const tr = el("tr", "event", el("td", "time", el("span", "chev", icon("chevron")), when(r.time)),
    cell("", el("span", "event-word", mark(r.group), el("span", "", r.what)), r.what), cell("title-cell", r.title), cell("", r.proposer), menu);
  const more_ = el("tr", "event-detail", detail);
  const body = el("tbody", "", tr, more_);
  disclose(tr, more_, body);
  return body;
}

// breakdown is each group's share of the events in the date range.
function breakdown(rows) {
  const total = rows.length;
  return el("div", "breakdown", [...groups, other].map((g) => {
    const n = count(rows, (r) => r.group === g);
    const pct = total ? Math.round((n / total) * 100) : 0;
    const bar = el("span", "bar-fill tone-fill " + g.tone);
    bar.style.width = pct + "%";
    return el("div", "breakdown-row", el("span", "dot tone-fill " + g.tone), el("span", "breakdown-label", g.label),
      el("span", "breakdown-count", String(n)), el("span", "breakdown-pct", pct + "%"), el("span", "bar", bar));
  }));
}

function recent(rows) {
  const last = rows.slice(-5).reverse();
  if (!last.length) return el("p", "empty", "empty...");
  return el("ul", "recent", last.map((r) => {
    const a = el("a", "recent-row", mark(r.group), el("span", "recent-time", when(r.time)),
      el("span", "recent-words", el("strong", "", r.what), el("span", "", r.title)));
    a.href = r.target ? "#/record/" + encodeURIComponent(r.target) : "#/history";
    a.title = [r.what, r.title].filter(Boolean).join("\n");
    return el("li", "", a);
  }));
}

export async function renderHistory() {
  const act = await activity();
  const all = act.rows;
  const series = (test) => perDay(all.filter(test).map((r) => r.time));
  const stat = (g, value, note) => statCard({ glyph: g.icon === "dot" ? "progress" : g.icon, tone: g.tone, value, label: g.label, note, noteTone: "up", layout: "compact",
    spark: sparkline(series((r) => r.group === g), g.tone, true, `${g.label} per day, last 14 days`) });
  const week = count(all, (r) => within(r.time, 7));
  const totalCard = statCard({ glyph: "progress", tone: "blue", value: all.length, label: "Total events", note: el("span", "", icon("up"), `+${week} this week`), noteTone: "up",
    layout: "compact", spark: sparkline(series(() => true), "blue", true, "Events per day, last 14 days") });

  const search = el("input", "search-input");
  Object.assign(search, { type: "search", placeholder: "Search history...", value: state.search, spellcheck: false });
  search.setAttribute("aria-label", "Search history");
  const controls = el("div", "filter-controls");
  const log = el("section", "panel log");
  const side = el("div", "history-side");

  function paint(reset) {
    if (reset) search.value = state.search;
    const q = state.search.trim().toLowerCase();
    const ranged = all.filter(inRange);
    const shown = ranged.filter((r) => inFamily(state.family, r.type) && inType(r)
      && (!q || [r.what, r.title, r.proposer, r.approver, r.reason, r.target].some((t) => (t || "").toLowerCase().includes(q))));
    if (state.sort === "newest") shown.reverse();
    const set = (key, value) => () => { state[key] = value; paint(); };
    const extras = [...new Set(all.filter((r) => r.group === other).map((r) => r.what))];
    const otherType = (what) => all.find((r) => r.what === what)?.type;
    const moreOptions = [["other", "All other events", count(all, (r) => r.group === other)], ...extras.map((w) => [otherType(w), w, count(all, (r) => r.what === w)])];
    const moreChosen = moreOptions.some((o) => o[0] === state.type);
    controls.replaceChildren(
      dropdown({ glyph: "list", options: families, value: state.family, onPick: (v) => { state.family = v; paint(); } }),
      dropdown({ glyph: "calendar", options: ranges, value: state.range, onPick: (v) => { state.range = v; paint(); } }),
      el("div", "chiprow events", chip("All", state.type === "all", set("type", "all")),
        ...groups.map((g) => chip(g.label, state.type === g.key, set("type", g.key), el("span", "dot tone-fill " + g.tone))),
        dropdown({ options: moreOptions, value: state.type, placeholder: "More", onPick: (v) => { state.type = v; paint(); }, className: moreChosen ? "chosen" : "" })));
    log.replaceChildren(
      head("history", "blue", "History log", `Showing ${shown.length} of ${all.length} events`,
        dropdown({ glyph: "sort", options: sorts, value: state.sort, onPick: (v) => { state.sort = v; paint(); } })),
      tablePanel("history", ["Time", "Event", "Record", "By", ""], ["var(--time-col)", "190px", "", "150px", "52px"],
        shown.map(logRow), shown.length ? null : el("p", "empty", "empty...")));
    side.replaceChildren(
      el("section", "panel", head("bars", "blue", "Event breakdown", null,
        dropdown({ options: ranges, value: state.range, onPick: (v) => { state.range = v; paint(); }, className: "small" })), breakdown(ranged)),
      el("section", "panel", head("bolt", "blue", "Recent activity", null, more("View all", () => {
        Object.assign(state, { search: "", family: "all", range: "all", type: "all", sort: "newest" });
        paint(true);
      })), recent(all)));
  }
  search.addEventListener("input", () => { state.search = search.value; paint(); });
  paint();

  return el("div", "screen history-screen",
    act.result === "UNKNOWN" ? el("p", "notice", "UNKNOWN: " + act.reason) : null,
    hero("history", "History", icon("history", "hero-icon"), ["A running log of everything that's happened in ", project(), "."], "Track changes, follow progress, and see the full story."),
    el("div", "stats six", totalCard, ...groups.map((g) => stat(g, count(all, (r) => r.group === g))), stat(other, count(all, (r) => r.group === other))),
    el("div", "panel filter-bar", el("label", "search", icon("search"), search), controls),
    el("div", "history-body", log, side));
}
