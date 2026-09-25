// The viewer's inline SVG icons and the words and colours every screen gives
// a record kind or an event type. Only drawing and naming live here: no
// data is read and nothing is rendered from the ledger.
const NS = "http://www.w3.org/2000/svg";

// Line icons on a 24-unit grid, drawn with the current text colour.
const paths = {
  progress: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7v5l3 2",
  back: "M9 10l-5 5 5 5M4 15h11a5 5 0 0 0 0-10h-3",
  blocked: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM5.6 5.6l12.8 12.8",
  ready: "M5 21V4M5 4h12l-2.5 4 2.5 4H5",
  decision: "M12 21V3M6 5h10l3 3-3 3H6zM18 13H8l-3 3 3 3h10z",
  proposal: "M3 13h5l1.5 3h5l1.5-3h5M5.5 5h13L21 13v6H3v-6z",
  chevron: "M9 6l6 6-6 6",
  down: "M6 9l6 6 6-6",
  copy: "M9 9h10v12H9zM5 15V3h10",
  open: "M14 4h6v6M20 4l-9 9M18 14v6H4V6h6",
  list: "M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01",
  grid: "M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z",
  check: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM8 12.5l2.7 2.7L16 9.8",
  claim: "M4 5h5v5H4zM4 14h5v5H4zM13 7h7M13 16h7",
  instrument: "M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h6",
  box: "M12 3l8 4.5v9L12 21l-8-4.5v-9zM4 7.5l8 4.5 8-4.5M12 12v9",
  source: "M4 5h16v11H9l-5 4z",
  review: "M4 5h16v11H9l-5 4zM12 8v3M12 13.5h.01",
  search: "M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM20 20l-4-4",
  plus: "M12 5v14M5 12h14",
  sort: "M8 4v16M4.5 7.5L8 4l3.5 3.5M16 20V4M12.5 16.5L16 20l3.5-3.5",
  filter: "M4 5h16l-6 7.5V19l-4 1.5v-8z",
  bars: "M5 20V10M10 20V4M15 20v-7M20 20v-4",
  refresh: "M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6",
  calendar: "M4 6h16v14H4zM4 10h16M8 3v4M16 3v4",
  history: "M4 12a8 8 0 1 0 2.3-5.7M4 4v5h5M12 8v4l3 2",
  play: "M8 5.5v13l10.5-6.5z",
  dots: "M5 12h.01M12 12h.01M19 12h.01",
  bolt: "M13 3L5 14h6l-1 7 8-11h-6z",
  target: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM12 7a5 5 0 1 0 0 10 5 5 0 0 0 0-10zM12 11.5h.01",
  file: "M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h4",
  up: "M12 19V5M6 11l6-6 6 6",
  arrow: "M5 12h14M13 6l6 6-6 6",
  close: "M6 6l12 12M18 6L6 18",
  tick: "M7 12.5l3.2 3.2L17 9",
};

// icon draws one line icon; className adds to "icon" (e.g. "icon solid").
export function icon(name, className = "") {
  const svg = document.createElementNS(NS, "svg");
  svg.setAttribute("viewBox", "0 0 24 24");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", ("icon " + className).trim());
  const path = document.createElementNS(NS, "path");
  path.setAttribute("d", paths[name]);
  svg.append(path);
  return svg;
}

// svg builds one SVG element with attributes, for the hero and sparklines.
export function svg(tag, attrs = {}, ...children) {
  const node = document.createElementNS(NS, tag);
  for (const [k, v] of Object.entries(attrs)) node.setAttribute(k, String(v));
  node.append(...children);
  return node;
}

// Each record kind: its plural tab name, its icon and its colour.
export const kinds = {
  task: { one: "Task", many: "Tasks", icon: "check", tone: "green" },
  claim: { one: "Claim", many: "Claims", icon: "claim", tone: "purple" },
  decision: { one: "Decision", many: "Decisions", icon: "decision", tone: "violet" },
  instrument: { one: "Instrument", many: "Instruments", icon: "instrument", tone: "amber" },
  source: { one: "Source", many: "Sources", icon: "source", tone: "sky" },
};

// Each event type in plain words, with the group the history screen counts
// it in and the family its type belongs to.
export const happened = {
  "task.create": "Task created", "task.amend": "Task changed", "task.start": "Work started", "attempt.terminal": "Handed back",
  "task.close": "Task closed", "blocker.hold": "Put on hold", "blocker.clear": "Hold cleared", "claim.assert": "Claim made",
  "claim.revise": "Claim changed", "criterion.fix": "Criterion set", "invocation.start": "Run started", "invocation.seal": "Run finished",
  "proof.admit": "Proof recorded", "decision.open": "Decision opened", "decision.revise": "Decision changed", "decision.dispose": "Decision made",
  "instrument.declare": "Instrument added", "instrument.revise": "Instrument changed", "trust.withdraw": "Trust withdrawn",
  "source.intake": "Source captured", supersede: "Superseded", correction: "Corrected", "artifact.dispose": "Evidence disposed",
};

// The four event groups the history screen names, the rest being "Other".
export const groups = [
  { key: "task.close", label: "Task closed", tone: "green", mark: "check", icon: "check" },
  { key: "task.amend", label: "Task changed", tone: "amber", mark: "dot", icon: "dot" },
  { key: "task.start", label: "Work started", tone: "purple", mark: "play", icon: "play" },
  { key: "attempt.terminal", label: "Handed back", tone: "red", mark: "back", icon: "back" },
];
export const other = { key: "other", label: "Other events", tone: "slate", mark: "dot", icon: "dots" };
export const groupOf = (type) => groups.find((g) => g.key === type) || other;

// families sort event types into the history screen's "events" menu.
export const families = [
  ["all", "All events"], ["task", "Task events"], ["claim", "Claim events"], ["decision", "Decision events"],
  ["instrument", "Instrument events"], ["source", "Source events"], ["review", "Reviews"],
];
const familyTypes = {
  task: ["task.", "attempt.", "blocker."], claim: ["claim.", "criterion.", "invocation.", "proof."], decision: ["decision."],
  instrument: ["instrument.", "trust."], source: ["source."], review: ["review."],
};
export const inFamily = (family, type) => family === "all" || (familyTypes[family] || []).some((p) => type.startsWith(p));

// mark is an event's small coloured sign: a check, a play, a back arrow or a dot.
export function mark(group) {
  const node = document.createElement("span");
  node.className = "mark tone-fill " + group.tone + " " + group.mark;
  const glyph = { check: "tick", play: "play", back: "back" }[group.mark];
  if (glyph) node.append(icon(glyph, group.mark === "play" ? "solid" : ""));
  return node;
}
