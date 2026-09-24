// Records, "All records": count tiles from bare show's summary, kind tabs,
// and one table row per current record and admitted source, each status the
// record's own, labelled. A row opens the record's detail (ui-detail.js).
import { el, link, view, status, kindOf, actorName } from "./ui.js";

const tabs = [["all", "All"], ["task", "Tasks"], ["claim", "Claims"], ["decision", "Decisions"], ["instrument", "Instruments"], ["source", "Sources"]];
const sum = (counts) => Object.values(counts || {}).reduce((a, b) => a + b, 0);
const yesNo = (s) => ({ true: "Yes", false: "No" }[s] || "UNKNOWN");

function tile(tone, count, label) {
  return el("div", "tile tone-" + tone, el("strong", "", String(count)), el("span", "", label));
}

// rowsOf is every record and source as one table row.
function rowsOf(show) {
  const records = show.records.map((r) => ({ kind: r.fact.kind.toLowerCase(), id: r.ref.record_id, title: r.label, record: r }));
  const sources = (show.sources || []).map((s) => ({
    kind: "source", id: s.intake.source_id,
    title: `${actorName(s.intake.speaker)} said, ${s.intake.length} bytes (${s.intake.source_ref?.content?.media_type || s.intake.source_ref?.kind || "UNKNOWN"})`,
    record: s,
  }));
  return records.concat(sources);
}

export async function renderRecords(tab) {
  const show = await view("show");
  const s = show.summary;
  const rows = rowsOf(show);
  const instruments = rows.filter((r) => r.kind === "instrument").length;
  const shown = tab === "all" ? rows : rows.filter((r) => r.kind === tab);

  const table = el("table", "records",
    el("thead", "", el("tr", "", ["ID", "Title", "Kind", "Status", "Author", "Self-approved"].map((h) => el("th", "", h)))),
    el("tbody", "", shown.length ? shown.map((r) => {
      const [label, tone] = status(r.record);
      const tr = el("tr", "",
        el("td", "", link(r.id)),
        el("td", "title-cell", r.title),
        el("td", "", kindOf(r.record)),
        el("td", "tone-text " + tone, label),
        el("td", "", actorName(r.record.author?.actor)),
        el("td", "", yesNo(r.record.self_admitted)));
      tr.addEventListener("click", (e) => { if (e.target.tagName !== "A") location.hash = "#/record/" + encodeURIComponent(r.id); });
      return tr;
    }) : el("tr", "", el("td", "none", "none"))));
  table.querySelector("tbody td.none")?.setAttribute("colspan", "6");

  return el("div", "screen",
    el("div", "page-head", el("h1", "", "All records"), el("p", "lede", "Browse the complete ledger. Everything is read-only.")),
    el("div", "tiles",
      tile("blue", sum(s.tasks), "Tasks"),
      tile("teal", sum(s.claims), "Claims"),
      tile("purple", sum(s.decisions), "Decisions"),
      tile("peach", instruments, "Instruments"),
      tile("sky", s.runs?.total ?? 0, "Runs"),
      tile("gray", s.sources ?? 0, "Sources")),
    el("nav", "tabs", tabs.map(([key, name]) => {
      const a = el("a", key === tab ? "active" : "", name);
      a.href = "#/records?tab=" + key;
      return a;
    })),
    el("div", "table-wrap", table));
}
