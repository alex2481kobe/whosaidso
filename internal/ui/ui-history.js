// History: stat cards per event group with their events per day, the filter
// bar (search, one event filter, the date range), the history log and, on
// the right, the event breakdown of the date range. All filtering happens
// here over the history view's events (ui-derive.js). A log row is one line:
// when, what happened in plain words, the record it names and who proposed
// it. An opened row shows its title and proposer whole, then who proposed
// and who approved it, the review's reason and the record. Narrow windows
// stack each row (ui-screens.css); the log is the screen's only scroller.
import { el, when, copyButton, openButton, facts, disclose, icon, tablePanel, project } from "./ui.js";
import { activity, perDay, within, count } from "./ui-derive.js";
import { hero, statCard, sparkline, head, chip, dropdown, rowMenu } from "./ui-parts.js";
import { groups, other, mark } from "./ui-icons.js";

// state survives re-renders from the live poll, so a filter stays put. event
// is the ONE event filter: "all", a group's key (its event type), "other"
// (every event outside the groups) or one other event's words. The chips and
// the event menu both set it.
const state = { search: "", event: "all", range: "all", sort: "newest" };
const ranges = [["7", "Last 7 days"], ["30", "Last 30 days"], ["all", "All time"]];
// The log is in ledger order, so newest means latest recorded, whatever the
// capture times beside the rows say.
const sorts = [["newest", "Newest first"], ["oldest", "Oldest first"]];
const orderNote = { newest: "latest recorded first", oldest: "earliest recorded first" };
const inRange = (r) => state.range === "all" || within(r.time, Number(state.range));
const matches = (event) => (r) => event === "all" || (event === "other" ? r.group === other
  : groups.some((g) => g.key === event) ? r.type === event : r.group === other && r.what === event);

function logRow(r) {
  const detail = el("td", "", facts([["Proposed by", r.proposer], [r.approverLabel, r.approver], ["Reason", r.reason],
    ["Record", r.target ? el("span", "id-line", openButton(r.target), copyButton("Copy id", r.target), el("span", "mono muted", r.target)) : ""]], "facts wide"));
  detail.colSpan = 5;
  const cell = (className, content, text) => { const td = el("td", className, content); td.title = text ?? content; return td; };
  const menu = el("td", "row-actions", r.target ? rowMenu("Event actions", [["Open record", () => { location.hash = "#/record/" + encodeURIComponent(r.target); }],
    ["Copy id", () => navigator.clipboard.writeText(r.target).catch(() => {})]]) : null);
  menu.addEventListener("click", (e) => e.stopPropagation());
  const tr = el("tr", "event", el("td", "time", el("span", "chev", icon("chevron")), when(r.time)),
    cell("what-cell", el("span", "event-word", mark(r.group), el("span", "", r.what)), r.what), cell("title-cell", r.title), cell("by-cell", r.proposer), menu);
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

export async function renderHistory() {
  const act = await activity();
  const all = act.rows;
  const series = (test) => perDay(all.filter(test).map((r) => r.time));
  const perDayLabel = (label) => `${label} per day, last 14 days`;
  const stat = (g, value) => statCard({ glyph: g.icon === "dot" ? "progress" : g.icon, tone: g.tone, value, label: g.label, layout: "compact",
    spark: sparkline(series((r) => r.group === g), g.tone, true, perDayLabel(g.label + " events")) });
  const week = count(all, (r) => within(r.time, 7));
  const totalCard = statCard({ glyph: "progress", tone: "blue", value: all.length, label: "Total events", note: el("span", "", icon("up"), `+${week} this\u00a0week`), noteTone: "up",
    layout: "compact", spark: sparkline(series(() => true), "blue", true, perDayLabel("Events")) });

  const search = el("input", "search-input");
  Object.assign(search, { type: "search", placeholder: "Search history...", value: state.search, spellcheck: false });
  search.setAttribute("aria-label", "Search history");
  const controls = el("div", "filter-controls");
  const log = el("section", "panel log");
  const side = el("section", "panel breakdown-panel");

  function paint() {
    const q = state.search.trim().toLowerCase();
    const ranged = all.filter(inRange);
    const shown = ranged.filter((r) => matches(state.event)(r)
      && (!q || [r.what, r.title, r.proposer, r.approver, r.reason, r.target].some((t) => (t || "").toLowerCase().includes(q))));
    if (state.sort === "newest") shown.reverse();
    const pick = (key) => (v) => { state[key] = v; paint(); };
    // the menu counts what each choice would show in the date range
    const n = (event) => count(ranged, matches(event));
    const extras = [...new Set(all.filter((r) => r.group === other).map((r) => r.what))];
    const options = [["all", "All events", n("all")], ...groups.map((g) => [g.key, g.label, n(g.key)]),
      ["other", "All other events", n("other")], ...extras.map((w) => [w, w, n(w)])];
    const sort = dropdown({ glyph: "sort", options: sorts, value: state.sort, onPick: pick("sort") });
    sort.title = "Ordered by when each event was recorded in the ledger";
    controls.replaceChildren(
      dropdown({ glyph: "list", options, value: state.event, onPick: pick("event"), className: "event-menu" + (state.event === "all" ? "" : " chosen") }),
      dropdown({ glyph: "calendar", options: ranges, value: state.range, onPick: pick("range") }),
      // wide screens show the common events as chips; narrower ones fold them into the menu
      el("div", "chiprow event-chips", groups.map((g) => chip(g.label, state.event === g.key,
        () => pick("event")(state.event === g.key ? "all" : g.key), el("span", "dot tone-fill " + g.tone)))));
    log.replaceChildren(
      head("history", "blue", "History log", `Showing ${shown.length} of ${all.length} events, ${orderNote[state.sort]}`, sort),
      tablePanel("history", ["Time", "Event", "Record", "By", ""], ["var(--time-col)", "190px", "", "150px", "52px"],
        shown.map(logRow), shown.length ? null : el("p", "empty", "empty...")));
    side.replaceChildren(head("bars", "blue", "Event breakdown", ranges.find((r) => r[0] === state.range)[1]), breakdown(ranged));
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
