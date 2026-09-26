// Records: every current record and admitted source, with the search, the
// kind tabs, the views and status sidebar (a Filters disclosure above the
// list on narrow windows), the sort menu and the list or grid format.
// Filtering and sorting happen here in the browser over the show view's rows;
// each status is the record's own, labelled, in coloured text. A wide list row
// is one line; a cell too long for it ends in an ellipsis and holds its whole
// text as a tooltip. Narrow windows stack each row so the title keeps its
// width. A row opens its record; its id is copied on the record's page.
import { el, view, status, actorName, sourceTitle, fullTitle, tablePanel, when, icon } from "./ui.js";
import { activity, within, count } from "./ui-derive.js";
import { hero, head, chip, dropdown } from "./ui-parts.js";
import { kinds } from "./ui-icons.js";

// state survives re-renders from the live poll, so a filter stays put, and so
// does the narrow-window Filters disclosure. kind is one kind or "" for all.
const state = { view: "all", kind: "", statuses: new Set(), search: "", sort: "recent", layout: "list", filtersOpen: false };
// clearAll clears every filter: the view, the kind, the statuses and the search.
// Sort and layout are how the list is shown, not what it holds, so they stay.
const clearAll = () => { Object.assign(state, { view: "all", kind: "", search: "" }); state.statuses.clear(); };
const filtered = () => state.view !== "all" || state.kind !== "" || state.statuses.size > 0 || state.search.trim() !== "";
// OTHER stands for every status past the four most common, when there are more than five.
const OTHER = "\u0000other";
const yesNo = (s) => ({ true: "Yes", false: "No" }[s] || "UNKNOWN");
const sorts = [["recent", "Most recent"], ["oldest", "Oldest"], ["title", "Title A-Z"], ["kind", "Kind"], ["status", "Status"]];
const time = (d) => (d ? d.getTime() : null);
// byTime orders known dates by dir and puts unknown dates last either way.
const byTime = (dir) => (a, b) => (time(a.updated) === null) - (time(b.updated) === null) || dir * ((time(a.updated) || 0) - (time(b.updated) || 0));
const order = {
  recent: byTime(-1), oldest: byTime(1), title: (a, b) => a.title.localeCompare(b.title),
  kind: (a, b) => a.kind.localeCompare(b.kind) || byTime(-1)(a, b), status: (a, b) => a.status.localeCompare(b.status) || byTime(-1)(a, b),
};

// rowsOf is every record and source as one row with what the table shows.
function rowsOf(show, act) {
  const review = new Set((show.attention || []).map((a) => a.ref?.record_id));
  const records = show.records.map((r) => ({ kind: r.fact.kind.toLowerCase(), id: r.ref.record_id, title: fullTitle(r), record: r }));
  const sources = (show.sources || []).map((s) => ({ kind: "source", id: s.intake.source_id, title: sourceTitle(s), record: s }));
  return records.concat(sources).map((r) => {
    const [label, tone] = status(r.record);
    return { ...r, status: label, tone, author: actorName(r.record.author?.actor), self: yesNo(r.record.self_admitted),
      review: review.has(r.id), created: act.created.get(r.id) || null, updated: act.updated.get(r.id) || null };
  });
}

// kindCell is a kind's icon and word.
const kindCell = (kind) => el("span", "kind tone-text " + kinds[kind].tone, icon(kinds[kind].icon), el("span", "", kinds[kind].one));
const cell = (className, content, text) => { const td = el("td", className, content); td.title = text ?? content; return td; };
const open = (id) => { location.hash = "#/record/" + encodeURIComponent(id); };

function table(rows) {
  const body = el("tbody", "", rows.map((r) => {
    const tr = el("tr", "", cell("title-cell", r.title), cell("", kindCell(r.kind), kinds[r.kind].one), cell("tone-text " + r.tone, r.status),
      cell("author", r.author), cell("self", r.self), cell("muted updated", when(r.updated)));
    tr.tabIndex = 0;
    tr.addEventListener("click", () => open(r.id));
    tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(r.id); });
    return tr;
  }));
  const panel = tablePanel("records", ["Title", "Kind", "Status", "Author", "Self-approved", "Updated"], ["", "130px", "190px", "140px", "120px", "130px"],
    body, rows.length ? null : el("p", "empty", "empty..."));
  panel.classList.add("records-table");
  return panel;
}

function grid(rows) {
  if (!rows.length) return el("div", "cards-scroll", el("p", "empty", "empty..."));
  return el("div", "cards-scroll", el("div", "record-cards", rows.map((r) => {
    const card = el("a", "record-card", el("div", "record-card-top", kindCell(r.kind), el("span", "tone-text " + r.tone, r.status)),
      el("p", "record-card-title", r.title), el("span", "record-card-meta", "Self-approved: " + r.self),
      el("div", "record-card-foot", el("span", "", r.author), el("span", "", when(r.updated))));
    card.href = "#/record/" + encodeURIComponent(r.id);
    card.title = r.title;
    return card;
  })));
}

// statusList is each status with its tone and count, most common first;
// past five, the four most common and "Other" for the rest.
function statusList(rows) {
  const all = [...rows.reduce((m, r) => m.set(r.status, [r.tone, (m.get(r.status)?.[1] || 0) + 1]), new Map())].sort((a, b) => b[1][1] - a[1][1]);
  if (all.length <= 5) return all;
  return [...all.slice(0, 4), [OTHER, ["muted", all.slice(4).reduce((n, s) => n + s[1][1], 0)]]];
}

// side is the views and filters column; its counts are over every row.
function side(rows, recent, paint, statuses) {
  const toggle = (set, key) => { set.has(key) ? set.delete(key) : set.add(key); paint(); };
  const option = (selected, lead, text, n, onPick, role) => {
    const b = el("button", "side-row", lead, el("span", "side-text", text), el("span", "side-count", String(n)));
    b.type = "button";
    b.setAttribute(role === "checkbox" ? "aria-checked" : "aria-pressed", String(selected));
    if (role) b.setAttribute("role", role);
    b.addEventListener("click", onPick);
    return b;
  };
  // "Needs attention" counts the show view's attention items, which are not only reviews.
  const views = [["all", "All records", "file", rows.length], ["review", "Needs attention", "review", count(rows, (r) => r.review)], ["recent", "Recently added", "progress", recent]];
  const clear = el("button", "button wide", icon("refresh"), el("span", "", "Clear filters"));
  clear.type = "button";
  clear.addEventListener("click", () => { clearAll(); paint(true); });
  return el("aside", "panel side",
    el("h3", "side-head", icon("bars"), "Views"),
    views.map(([key, text, glyph, n]) => option(state.view === key, icon(glyph), text, n, () => { state.view = key; paint(); })),
    el("h3", "side-head ruled", icon("filter"), "Filters"),
    el("h4", "", "Status"),
    statuses.map(([label, [tone, n]]) => option(state.statuses.has(label), el("span", "dot tone-fill " + tone), label === OTHER ? "Other" : label, n, () => toggle(state.statuses, label), "checkbox")),
    clear);
}

export async function renderRecords() {
  const [show, act] = await Promise.all([view("show"), activity()]);
  const rows = rowsOf(show, act);
  const recent = count(rows, (r) => within(r.created, 7));

  const search = el("input", "search-input");
  Object.assign(search, { type: "search", id: "records-search", placeholder: "Search records...", value: state.search, spellcheck: false });
  search.setAttribute("aria-label", "Search records");
  const tabs = el("nav", "chiprow kind-tabs");
  tabs.setAttribute("aria-label", "Kind");
  const sidebar = el("div", "side-holder");
  sidebar.id = "records-filters";
  const listing = el("section", "panel listing");
  const body = el("div", "records-body", sidebar, listing);
  // On narrow windows the sidebar folds into this disclosure above the list,
  // closed until opened; wider windows hide the button and show the sidebar.
  const filterCount = el("span", "filters-count");
  const filters = el("button", "button filters-toggle", icon("filter"), el("span", "", "Filters"), filterCount, icon("down", "caret"));
  filters.type = "button";
  filters.setAttribute("aria-controls", sidebar.id);
  filters.addEventListener("click", () => { state.filtersOpen = !state.filtersOpen; paint(); });

  // "Other" is every status the sidebar does not name
  const statuses = statusList(rows);
  const top = statuses.map((s) => s[0]);
  const statusOk = (r) => !state.statuses.size || state.statuses.has(r.status) || (state.statuses.has(OTHER) && !top.includes(r.status));
  function paint(reset) {
    if (reset) search.value = state.search;
    const q = state.search.trim().toLowerCase();
    const shown = rows.filter((r) => (state.view !== "review" || r.review) && (state.view !== "recent" || within(r.created, 7))
      && (!state.kind || state.kind === r.kind) && statusOk(r)
      && (!q || [r.title, r.id, r.author, r.status, kinds[r.kind].one].some((t) => t.toLowerCase().includes(q)))).sort(order[state.sort]);
    // "All" is pressed only while nothing is filtered, and picking it clears every filter.
    tabs.replaceChildren(chip("All", !filtered(), () => { clearAll(); paint(true); }),
      ...Object.entries(kinds).map(([key, k]) => chip(k.many, state.kind === key, () => { state.kind = key; paint(); })));
    sidebar.replaceChildren(side(rows, recent, paint, statuses));
    // the count is the filters inside the disclosure: a view other than All records, and each status
    const inside = (state.view !== "all" ? 1 : 0) + state.statuses.size;
    filterCount.textContent = inside ? `(${inside})` : "";
    filters.setAttribute("aria-label", inside ? `Filters, ${inside} active` : "Filters");
    filters.setAttribute("aria-expanded", String(state.filtersOpen));
    body.classList.toggle("filters-open", state.filtersOpen);
    const layout = (key, glyph, label) => {
      const b = el("button", "icon-button", icon(glyph));
      b.type = "button";
      b.setAttribute("aria-label", label);
      b.setAttribute("aria-pressed", String(state.layout === key));
      b.addEventListener("click", () => { state.layout = key; paint(); });
      return b;
    };
    listing.replaceChildren(
      head("file", "blue", `Records (${shown.length})`, null,
        dropdown({ glyph: "sort", prefix: "Sort: ", options: sorts, value: state.sort, onPick: (v) => { state.sort = v; paint(); } }),
        el("div", "segmented", layout("list", "list", "List"), layout("grid", "grid", "Grid"))),
      state.layout === "grid" ? grid(shown) : table(shown));
  }
  search.addEventListener("input", () => { state.search = search.value; paint(); });
  paint();

  return el("div", "screen records",
    hero("records", "Records", null, ["Browse all records in ", show.project, "."], "Tasks, claims, decisions, instruments, and sources, all in one place."),
    el("div", "records-top", el("label", "search", icon("search"), search), tabs),
    filters, body);
}
