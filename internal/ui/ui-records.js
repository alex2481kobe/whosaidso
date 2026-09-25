// Records: every current record and admitted source, with the stat cards,
// search, kind tabs, the views and filters sidebar, the sort menu and the
// list or grid format. Filtering and sorting happen here in the browser over
// the show view's rows; each status is the record's own, labelled, in
// coloured text. A list row is one line; a cell too long for it ends in an
// ellipsis and holds its whole text as a tooltip. A row opens its record.
import { el, view, status, actorName, sourceTitle, fullTitle, tablePanel, when, icon } from "./ui.js";
import { activity, within, count } from "./ui-derive.js";
import { hero, statCard, head, chip, dropdown, rowMenu } from "./ui-parts.js";
import { kinds } from "./ui-icons.js";

// state survives re-renders from the live poll, so a filter stays put.
const state = { view: "all", kinds: new Set(), statuses: new Set(), search: "", sort: "recent", layout: "list" };
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

// actions is a row's "more" menu: open the record or copy its id.
function actions(id) {
  const td = el("td", "row-actions", rowMenu("Record actions", [["Open record", () => open(id)],
    ["Copy id", () => navigator.clipboard.writeText(id).catch(() => {})]]));
  td.addEventListener("click", (e) => e.stopPropagation());
  return td;
}

function table(rows) {
  const body = el("tbody", "", rows.map((r) => {
    const tr = el("tr", "", cell("title-cell", r.title), cell("", kindCell(r.kind), kinds[r.kind].one), cell("tone-text " + r.tone, r.status),
      cell("", r.author), cell("", r.self), cell("muted", when(r.updated)), actions(r.id));
    tr.tabIndex = 0;
    tr.addEventListener("click", () => open(r.id));
    tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(r.id); });
    return tr;
  }));
  return tablePanel("records", ["Title", "Kind", "Status", "Author", "Self-approved", "Updated", ""], ["", "130px", "190px", "140px", "120px", "130px", "52px"],
    body, rows.length ? null : el("p", "empty", "empty..."));
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
  const views = [["all", "All records", "file", rows.length], ["review", "Needs review", "review", count(rows, (r) => r.review)], ["recent", "Recently added", "progress", recent]];
  const clear = el("button", "button wide", icon("refresh"), el("span", "", "Clear filters"));
  clear.type = "button";
  clear.addEventListener("click", () => { Object.assign(state, { view: "all", search: "" }); state.kinds.clear(); state.statuses.clear(); paint(true); });
  return el("aside", "panel side",
    el("h3", "side-head", icon("bars"), "Views"),
    views.map(([key, text, glyph, n]) => option(state.view === key, icon(glyph), text, n, () => { state.view = key; paint(); })),
    el("h3", "side-head ruled", icon("filter"), "Filters"),
    el("h4", "", "Kind"),
    Object.entries(kinds).map(([key, k]) => option(state.kinds.has(key), el("span", "box"), k.many, count(rows, (r) => r.kind === key), () => toggle(state.kinds, key), "checkbox")),
    el("h4", "", "Status"),
    statuses.map(([label, [tone, n]]) => option(state.statuses.has(label), el("span", "dot tone-fill " + tone), label === OTHER ? "Other" : label, n, () => toggle(state.statuses, label), "checkbox")),
    clear);
}

export async function renderRecords() {
  const [show, act] = await Promise.all([view("show"), activity()]);
  const rows = rowsOf(show, act);
  const recent = count(rows, (r) => within(r.created, 7));
  const byKind = (k) => rows.filter((r) => r.kind === k);
  const tasks = byKind("task"), claims = byKind("claim"), instruments = byKind("instrument");
  const closed = count(tasks, (r) => r.record.task?.status === "CLOSED");
  const withdrawn = count(instruments, (r) => r.status === "Withdrawn");

  const search = el("input", "search-input");
  Object.assign(search, { type: "search", id: "records-search", placeholder: "Search records...", value: state.search, spellcheck: false });
  search.setAttribute("aria-label", "Search records");
  // New record has no write path yet: it says so in a small note under it
  // that closes on the next click anywhere.
  const note = el("span", "soon-note", "Adding records from the UI is coming later.");
  note.hidden = true;
  const add = el("button", "primary", icon("plus"), el("span", "", "New record"), el("span", "primary-split", icon("down")));
  add.type = "button";
  add.addEventListener("click", (e) => {
    e.stopPropagation();
    note.hidden = !note.hidden;
    if (!note.hidden) document.addEventListener("click", () => { note.hidden = true; }, { once: true });
  });
  const tabs = el("nav", "chiprow");
  const sidebar = el("div", "side-holder");
  const listing = el("section", "panel listing");

  // "Other" is every status the sidebar does not name
  const statuses = statusList(rows);
  const top = statuses.map((s) => s[0]);
  const statusOk = (r) => !state.statuses.size || state.statuses.has(r.status) || (state.statuses.has(OTHER) && !top.includes(r.status));
  function paint(reset) {
    if (reset) search.value = state.search;
    const q = state.search.trim().toLowerCase();
    const shown = rows.filter((r) => (state.view !== "review" || r.review) && (state.view !== "recent" || within(r.created, 7))
      && (!state.kinds.size || state.kinds.has(r.kind)) && statusOk(r)
      && (!q || [r.title, r.id, r.author, r.status, kinds[r.kind].one].some((t) => t.toLowerCase().includes(q)))).sort(order[state.sort]);
    const one = state.kinds.size === 1 ? [...state.kinds][0] : "";
    tabs.replaceChildren(chip("All", !state.kinds.size, () => { state.kinds.clear(); paint(); }),
      ...Object.entries(kinds).map(([key, k]) => chip(k.many, one === key, () => { state.kinds.clear(); state.kinds.add(key); paint(); })));
    sidebar.replaceChildren(side(rows, recent, paint, statuses));
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
    hero("records", "Records", null, ["Browse and manage all records in ", show.project, "."], "Tasks, claims, decisions, instruments, and sources, all in one place."),
    el("div", "records-top",
      el("div", "stats four",
        statCard({ glyph: "list", tone: "blue", value: rows.length, label: "Total records", note: `Across ${new Set(rows.map((r) => r.kind)).size} kinds`, layout: "ruled" }),
        statCard({ glyph: "check", tone: "green", value: closed, label: "Closed", note: tasks.length ? `${Math.round((closed / tasks.length) * 100)}% of tasks` : "No tasks yet", layout: "ruled" }),
        statCard({ glyph: "claim", tone: "violet", value: claims.length, label: "Claims", note: claims.length ? `${count(claims, (r) => r.record.claim?.status === "PROVEN")} proven` : "None yet", layout: "ruled" }),
        statCard({ glyph: "instrument", tone: "amber", value: instruments.length, label: "Instruments", note: !instruments.length ? "None yet" : withdrawn ? `${withdrawn} withdrawn` : "All active", layout: "ruled" })),
      el("div", "records-tools",
        el("div", "tool-line", el("label", "search", icon("search"), search, el("kbd", "", "⌘ K")), el("div", "new-record", add, note)),
        el("div", "tool-line", tabs))),
    el("div", "records-body", sidebar, listing));
}

// ⌘K (or Ctrl+K) puts the cursor in the records search when it is on screen.
document.addEventListener("keydown", (e) => {
  const input = document.getElementById("records-search");
  if (input && (e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") { e.preventDefault(); input.focus(); input.select(); }
});
