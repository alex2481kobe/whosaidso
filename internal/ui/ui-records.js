// Records: the kind filter and one table row per current record and admitted
// source, title first, each status the record's own, labelled. A row opens
// the record's detail (ui-detail.js).
import { el, view, status, kindOf, actorName, sourceTitle } from "./ui.js";

const kinds = [["all", "All"], ["task", "Tasks"], ["claim", "Claims"], ["decision", "Decisions"], ["instrument", "Instruments"], ["source", "Sources"]];
const yesNo = (s) => ({ true: "Yes", false: "No" }[s] || "UNKNOWN");

// rowsOf is every record and source as one table row.
function rowsOf(show) {
  const records = show.records.map((r) => ({ kind: r.fact.kind.toLowerCase(), id: r.ref.record_id, title: r.label, record: r }));
  const sources = (show.sources || []).map((s) => ({ kind: "source", id: s.intake.source_id, title: sourceTitle(s), record: s }));
  return records.concat(sources);
}

export async function renderRecords(kind) {
  const rows = rowsOf(await view("show"));
  const shown = kind === "all" ? rows : rows.filter((r) => r.kind === kind);
  const table = el("table", "table",
    el("thead", "", el("tr", "", ["Title", "Kind", "Status", "Author", "Self-approved"].map((h) => el("th", "", h)))),
    el("tbody", "", shown.map((r) => {
      const [label, tone] = status(r.record);
      const tr = el("tr", "", el("td", "title-cell", r.title), el("td", "", kindOf(r.record)),
        el("td", "tone-text " + tone, label), el("td", "", actorName(r.record.author?.actor)), el("td", "", yesNo(r.record.self_admitted)));
      tr.tabIndex = 0;
      const open = () => { location.hash = "#/record/" + encodeURIComponent(r.id); };
      tr.addEventListener("click", open);
      tr.addEventListener("keydown", (e) => { if (e.key === "Enter") open(); });
      return tr;
    })));
  return el("div", "screen fill",
    el("nav", "chipstrip", kinds.map(([key, name]) => {
      const a = el("a", "chip", name);
      a.href = "#/records?tab=" + key;
      a.setAttribute("aria-selected", String(key === kind));
      return a;
    })),
    el("div", "table-panel", table, shown.length ? null : el("p", "empty", "Empty")));
}
