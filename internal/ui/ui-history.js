// History: the history view's events newest first, each with the record it
// names, who wrote it, who admitted it and the review's reason; "One record"
// is history of one id. Also the list of every project on this machine,
// shown when the viewer runs outside a project.
import { el, link, art, view, times, when, actorName } from "./ui.js";

// recordOf is the record id an event names, read from its own fields.
function recordOf(data) {
  return data.id || data.source_id || data.target?.record_id || data.task?.record_id || data.claim?.record_id
    || data.envelope?.criterion_ref?.value?.claim?.record_id || data.hold?.task?.record_id || "";
}

// family picks the icon colour from the event type's first word.
const tones = { task: "green", attempt: "red", blocker: "red", claim: "amber", criterion: "amber", proof: "amber",
  invocation: "blue", decision: "purple", instrument: "teal", source: "gray", review: "gray", artifact: "gray" };
const glyphs = { green: "✓", red: "↩", amber: "◆", blue: "▣", purple: "⬢", teal: "◎", gray: "•" };

export async function renderHistory(id, lastRecord) {
  const one = id || "";
  const [answer, at] = await Promise.all([view("history", one ? { id: one } : {}), times()]);
  const all = one ? await view("history") : answer;
  // Every bundle's review gives its reason; a bundle holding only a review
  // (a rejected or correction-requested packet) keeps its own row.
  const reviews = new Map();
  const others = new Set();
  for (const row of all.events) {
    if (row.event.type === "review.admit") reviews.set(row.origin.sequence, row.event.data);
    else others.add(row.origin.sequence);
  }
  const rows = answer.events.filter((r) => r.event.type !== "review.admit" || !others.has(r.origin.sequence)).reverse().map((row) => {
    const data = row.event.data;
    const review = reviews.get(row.origin.sequence);
    const family = row.event.type.split(".")[0];
    const tone = tones[family] || "gray";
    const target = recordOf(data);
    const reason = row.event.type === "review.admit" ? `${data.outcome}: ${data.reason}` : review?.reason;
    return el("li", "",
      el("span", "tl-dot"), el("span", "tl-time", when(at(row.origin))),
      el("span", "event-icon tone-" + tone, glyphs[tone]),
      el("strong", "event-type", row.event.type),
      target ? link(target) : el("span", "muted", "—"),
      el("div", "event-who", el("span", "", actorName(row.author?.actor)), el("span", "arrow", " → "), el("span", "", actorName(row.admitter)),
        reason ? el("p", "quote", `“${reason}”`) : null));
  });

  const toggle = el("div", "segmented");
  const allLink = el("a", one ? "" : "active", "All records");
  allLink.href = "#/history";
  const oneTarget = one || lastRecord;
  const oneLink = el("a", one ? "active" : "", "One record");
  if (oneTarget) oneLink.href = "#/history/" + encodeURIComponent(oneTarget);
  else oneLink.classList.add("disabled");
  toggle.append(allLink, oneLink);

  const note = one ? el("p", "lede", "Every revision and every event or run that names ", link(one), ".")
    : el("p", "lede", "A timeline of everything that's been recorded.");
  const owl = el("img", "footer-owl");
  owl.src = "/logo.svg";
  owl.alt = "";
  return el("div", "screen",
    el("div", "page-head row", el("div", "", el("h1", "", "History"), note), toggle),
    answer.result === "UNKNOWN" ? el("section", "notice", "UNKNOWN: " + answer.reason) : null,
    el("ol", "timeline history", rows.length ? rows : el("li", "none", "none")),
    el("section", "footer-card", owl, el("p", "", "A complete, append-only history.", el("br"), "No edits. Just the truth."),
      el("div", "caption", el("span", "scribble", "Good", el("br"), "engineering", el("br"), "leaves a trail."), art("sprig"))));
}

export function renderProjects(projects, select) {
  const rows = projects.map((p) => {
    const row = el("button", "project-row" + (p.available ? "" : " unavailable"),
      el("strong", "mono", p.id), el("span", "muted", p.home || "no home"),
      p.available ? el("span", "", `latest #${p.watermark.sequence} · ${p.totals.in_flight} in flight · ${p.totals.awaiting_acceptance} awaiting acceptance · ${p.totals.blocked} blocked · ${p.totals.ready} ready · ${p.totals.open_decisions} open decisions`)
        : el("span", "tone-text red", "Unavailable: " + p.reason));
    row.type = "button";
    row.disabled = !p.available;
    row.addEventListener("click", () => select(p.id));
    return row;
  });
  return el("div", "screen",
    el("div", "page-head", el("h1", "", "Projects"), el("p", "lede", "Every project this machine's WhoSaidSo home binds.")),
    el("div", "project-list", rows.length ? rows : el("p", "none", "none")));
}
