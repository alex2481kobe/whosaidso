// Gallery: the files an agent was asked to show, from whosaidso gallery list's
// own answer. Each card previews its file by the kind the server named
// (image, video, or text shown as text, never as markup), says Missing when
// the file is gone, and offers Reveal in Finder and Remove from gallery;
// Remove captures a retraction for admission and never touches the file. The search, the kind chips and the
// sort menu work here in the browser, with the same parts as Records: the
// search over title, note, path, author and date. Files are fetched by entry
// id only.
import { el, api, apiURL, send, project, when, stamp, icon } from "./ui.js";
import { head, hero, chip, dropdown } from "./ui-parts.js";

// state keeps the filters across re-renders from the live poll. They belong
// to the project they were set in: drawing the gallery of another project
// clears them. The sort stays.
const state = { project: "", search: "", kind: "", sort: "recent" };
// The kinds are the server's preview kinds; "other" is a file with none.
const kinds = [["image", "Images"], ["video", "Videos"], ["text", "Text"], ["other", "Other"]];
const kindOf = (entry) => entry.kind || "other";
// The list arrives oldest first; a stable sort keeps newest first among ties.
const sorts = [["recent", "Most recent"], ["oldest", "Oldest"], ["title", "Title A-Z"], ["kind", "Kind"]];
const order = {
  recent: (list) => list.slice().reverse(),
  oldest: (list) => list.slice(),
  title: (list) => order.recent(list).sort((a, b) => a.title.localeCompare(b.title)),
  kind: (list) => order.recent(list).sort((a, b) => kindOf(a).localeCompare(kindOf(b))),
};

// preview draws the file by its kind; a missing file or an unknown kind
// says so instead.
function preview(entry, src) {
  if (entry.status !== "present") return el("div", "gallery-none tone-text red", entry.status === "changed" ? "Changed" : "Missing");
  if (entry.kind === "image") {
    const img = el("img");
    Object.assign(img, { src, alt: entry.title, loading: "lazy" });
    return el("div", "gallery-media", img);
  }
  if (entry.kind === "video") {
    const video = el("video");
    Object.assign(video, { src, controls: true, preload: "metadata", playsInline: true });
    return el("div", "gallery-media", video);
  }
  if (entry.kind === "text") {
    const pre = el("pre", "gallery-text", "Loading...");
    fetch(src).then(async (r) => {
      if (!r.ok) throw new Error(`${r.status} ${r.statusText}`);
      const cut = r.headers.get("X-WhoSaidSo-Truncated");
      pre.textContent = (await r.text()) + (cut ? `\n\n[first ${Math.round(cut / 1024)} KiB shown]` : "");
    }).catch((e) => { pre.textContent = "Could not read the file: " + e.message; });
    return pre;
  }
  return el("div", "gallery-none muted", "No preview");
}

// action is a labelled button.
function action(glyph, label, onPick) {
  const button = el("button", "button", icon(glyph), el("span", "", label));
  button.type = "button";
  button.addEventListener("click", onPick);
  return button;
}

// removeButton is the card's corner X. It asks for a second click within
// three seconds, saying so beside the X, so a stray click cannot drop an
// entry.
function removeButton(onRemove) {
  const word = el("span", "gallery-remove-word");
  const button = el("button", "gallery-remove", icon("close"), word);
  button.type = "button";
  let armed = null;
  const show = (asking) => {
    word.textContent = asking ? "Click again to remove" : "";
    button.classList.toggle("armed", asking);
    button.title = asking ? "Click again to remove from gallery" : "Remove from gallery";
    button.setAttribute("aria-label", button.title);
  };
  show(false);
  button.addEventListener("click", () => {
    // The confirming click disarms, so a failed removal needs a fresh confirmation.
    if (armed) { clearTimeout(armed); armed = null; show(false); onRemove(); return; }
    show(true);
    armed = setTimeout(() => { armed = null; show(false); }, 3000);
  });
  return button;
}

// Fullscreen is a modal viewport on every device, with browser fullscreen when
// available. Card playback pauses while the enlarged copy is open.
function fullscreen(entry, src, original, trigger) {
  const video = original.querySelector("video");
  const time = video?.currentTime || 0;
  video?.pause();
  const content = preview(entry, src);
  const enlarged = content.querySelector?.("video");
  if (enlarged) enlarged.addEventListener("loadedmetadata", () => { enlarged.currentTime = time; }, { once: true });
  const close = action("close", "Close", () => dialog.close());
  const dialog = el("dialog", "gallery-fullscreen",
    el("div", "gallery-fullscreen-head", el("p", "gallery-title", entry.title), close), content);
  dialog.setAttribute("aria-label", entry.title);
  // Dialog elements cannot enter native fullscreen; their container can.
  const surface = el("div", "", dialog);
  // Only leaving fullscreen altogether closes the viewer: the enlarged
  // video's own fullscreen control stacks the video above the surface, and
  // that change bubbles here too.
  const changed = () => {
    if (!document.fullscreenElement && dialog.open) dialog.close();
  };
  surface.addEventListener("fullscreenchange", changed);
  dialog.addEventListener("close", () => {
    enlarged?.pause();
    if (video && enlarged?.readyState >= 1) video.currentTime = enlarged.currentTime;
    surface.removeEventListener("fullscreenchange", changed);
    if (document.fullscreenElement === surface) document.exitFullscreen().catch(() => {});
    surface.remove();
    // Return focus without scrolling: a trigger outside the viewport would
    // otherwise jump the gallery list away from where the reader was.
    if (trigger.isConnected) trigger.focus({ preventScroll: true });
  }, { once: true });
  document.body.append(surface);
  // The dialog opens only once the fullscreen request has settled: the top
  // layer stacks in the order things enter it, so a surface that goes
  // fullscreen after the dialog opened would cover it with its black
  // backdrop. A refused or unsupported request leaves the full-viewport modal.
  const open = () => { dialog.showModal(); close.focus(); };
  if (surface.requestFullscreen) surface.requestFullscreen().then(open, open);
  else open();
}

// card is one entry: its preview with the remove X in the corner, then its
// words and one row of buttons. Double-clicking an image opens it full
// screen. A failure is said above the cards.
function card(entry, said) {
  const params = { project: project(), id: entry.id };
  const added = new Date(entry.added_at);
  const path = el("span", "mono gallery-path", entry.path);
  path.title = entry.path;
  const fail = (e) => { said.textContent = `${entry.title}: ${e.message}`; };
  const reveal = action("open", "Reveal in Finder", () => send("/api/gallery/reveal", params).catch(fail));
  const remove = removeButton(() => send("/api/gallery/remove", params).then((receipt) => { said.textContent = `Retraction captured; admit with whosaidso admit ${receipt.captured} --outcome accepted --reason TEXT.`; }).catch(fail));
  const src = apiURL("/api/gallery/file", { ...params, cap: entry.cap });
  const media = preview(entry, src);
  const expand = entry.status === "present" && ["image", "video", "text"].includes(entry.kind)
    ? action("expand", "Full screen", () => fullscreen(entry, src, media, expand)) : null;
  media.querySelector?.("img")?.addEventListener("dblclick", () => fullscreen(entry, src, media, expand));
  const node = el("article", "gallery-card", media, remove,
    el("div", "gallery-card-body",
      el("p", "gallery-title", entry.title),
      el("p", "gallery-note", `${entry.verdict}${entry.verdict_by?.id ? " by " + entry.verdict_by.id : ""}${entry.verdict_note ? ": " + entry.verdict_note : ""}`),
      entry.note ? el("p", "gallery-note", entry.note) : null,
      path,
      el("div", "record-card-foot", el("span", "", entry.added_by || "UNKNOWN"), el("span", "", when(added))),
      el("div", "gallery-actions", expand, reveal)));
  node.dataset.kind = kindOf(entry);
  node.dataset.search = [entry.title, entry.note, entry.path, entry.added_by, entry.added_at, stamp(added), when(added)].join("\n").toLowerCase();
  return node;
}

// renderGallery draws the entries in the chosen order, then reads the gallery
// again every few seconds while this screen is shown (the ledger poll does not
// see gallery changes) and redraws only the entries that changed, so a
// playing video or a scrolled text keeps its place.
export async function renderGallery() {
  const answer = await api("/api/gallery", { project: project() });
  if (state.project !== project()) Object.assign(state, { project: project(), search: "", kind: "" });
  const said = el("p", "gallery-said tone-text red");
  const count = el("span", "count");
  const empty = el("p", "empty", "empty...");
  const cards = el("div", "gallery-cards");
  const filter = () => {
    const q = state.search.trim().toLowerCase();
    let shown = 0;
    for (const c of cards.children) {
      c.hidden = (q !== "" && !c.dataset.search.includes(q)) || (state.kind !== "" && c.dataset.kind !== state.kind);
      shown += c.hidden ? 0 : 1;
    }
    count.textContent = String(shown);
    empty.hidden = shown > 0;
  };
  const drawn = new Map(); // entry id -> { id, sig, node }
  let latest = []; // the entries last drawn, which a new sort redraws
  const draw = (entries) => {
    latest = entries;
    const want = order[state.sort](entries).map((entry) => {
      const sig = JSON.stringify(entry);
      const had = drawn.get(entry.id);
      if (had && had.sig === sig) return had;
      const node = card(entry, said);
      return { id: entry.id, sig, node };
    });
    const keep = new Set(want.map((w) => w.node));
    for (const [id, had] of drawn) if (!keep.has(had.node)) { had.node.remove(); drawn.delete(id); }
    // Insert new cards in place; a kept card is detached only when a new sort
    // moves it, since detaching a media element stops it.
    let at = cards.firstChild;
    for (const w of want) {
      drawn.set(w.id, w);
      if (w.node === at) at = at.nextSibling;
      else cards.insertBefore(w.node, at);
    }
    filter();
  };
  draw(answer.entries);
  // One read in flight at a time, given up after ten seconds; a tick while
  // one is out is skipped. A list read before the newest one applied, or
  // before a Remove on this screen, is older than what is drawn and is
  // dropped, so a removed card cannot come back from a late answer.
  const shown = project();
  let asked = 0, applied = 0, busy = false;
  const timer = setInterval(() => {
    if (!cards.isConnected || project() !== shown) { clearInterval(timer); return; }
    if (busy || document.visibilityState === "hidden") return;
    busy = true;
    const mine = ++asked;
    const stop = new AbortController();
    const limit = setTimeout(() => stop.abort(), 10000);
    api("/api/gallery", { project: shown }, stop.signal)
      .then((next) => { if (cards.isConnected && mine > applied) { applied = mine; draw(next.entries); } })
      .catch(() => {})
      .finally(() => { clearTimeout(limit); busy = false; });
  }, 3000);
  const search = el("input", "search-input");
  Object.assign(search, { type: "search", placeholder: "Search the gallery...", value: state.search, spellcheck: false });
  search.setAttribute("aria-label", "Search the gallery");
  search.addEventListener("input", () => { state.search = search.value; filter(); });
  const chips = el("nav", "chiprow gallery-kinds");
  chips.setAttribute("aria-label", "Kind");
  const sortMenu = el("div", "gallery-sort");
  const paint = () => {
    chips.replaceChildren(chip("All", state.kind === "", () => { state.kind = ""; paint(); filter(); }),
      ...kinds.map(([key, label]) => chip(label, state.kind === key, () => { state.kind = key; paint(); filter(); })));
    sortMenu.replaceChildren(dropdown({ glyph: "sort", prefix: "Sort: ", options: sorts, value: state.sort,
      onPick: (v) => { state.sort = v; paint(); draw(latest); } }));
  };
  paint();
  return el("div", "screen",
    hero("gallery", "Gallery", icon("file", "hero-icon"), ["What an agent was asked to show you in ", project(), "."],
      "Remove captures a retraction for admission; the file stays where it is."),
    el("section", "panel gallery",
      head("file", "sky", "Files", null, el("label", "search", icon("search"), search), sortMenu, count),
      chips,
      said,
      el("div", "gallery-scroll", cards, empty)));
}
