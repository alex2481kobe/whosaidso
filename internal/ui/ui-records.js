// Records: the kind filter and one table row per current record and admitted
// source, title first, each status the record's own, labelled. A row is one
// line; a cell too long for it ends in an ellipsis and holds its whole text
// as a tooltip. A row opens the record's detail (ui-detail.js).
import { el, view, status, kindOf, actorName, sourceTitle, fullTitle, tablePanel } from "./ui.js";

const kinds = [["all", "All"], ["task", "Tasks"], ["claim", "Claims"], ["decision", "Decisions"], ["instrument", "Instruments"], ["source", "Sources"]];
const yesNo = (s) => ({ true: "Yes", false: "No" }[s] || "UNKNOWN");
const cell = (className, text) => { const td = el("td", className, text); td.title = text; return td; };

// rowsOf is every record and source as one table row.
function rowsOf(show) {
  const records = show.records.map((r) => ({ kind: r.fact.kind.toLowerCase(), id: r.ref.record_id, title: fullTitle(r), record: r }));
  const sources = (show.sources || []).map((s) => ({ kind: "source", id: s.intake.source_id, title: sourceTitle(s), record: s }));
  return records.concat(sources);
}

export async function renderRecords(kind) {
  const rows = rowsOf(await view("show"));
  const shown = kind === "all" ? rows : rows.filter((r) => r.kind === kind);
  const body = el("tbody", "", shown.map((r) => {
      const [label, tone] = status(r.record);
      const tr = el("tr", "", cell("title-cell", r.title), cell("", kindOf(r.record)),
        cell("tone-text " + tone, label), cell("", actorName(r.record.author?.actor)), cell("", yesNo(r.record.self_admitted)));
      tr.tabIndex = 0;
      const open = () => { location.hash = "#/record/" + encodeURIComponent(r.id); };
      tr.addEventListener("click", open);
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
      return tr;
    }));
  return el("div", "screen fill",
    el("nav", "chipstrip", kinds.map(([key, name]) => {
      const a = el("a", "chip", name);
      a.href = "#/records?tab=" + key;
      a.setAttribute("aria-selected", String(key === kind));
      return a;
    })),
    tablePanel("", ["Title", "Kind", "Status", "Author", "Self-approved"], ["", "120px", "200px", "130px", "130px"],
      body, shown.length ? null : el("p", "empty", "empty...")));
}
