// All lists share early batch loading and explicit refresh. Appending never
// replaces existing rows or changes scroll or focus; filters may reset a list.
import { el } from "./ui.js";
import { iconButton, message } from "./ui-components.js";

export function scrollBatches({first, request, draw, active, stamp = page => page.revision || JSON.stringify(page.watermark)}) {
  const sentinel = el("div", "gallery-sentinel");
  const error = message("", "error"); error.hidden = true;
  let next, revision, busy = false, generation = 0, current;
  function apply(page, reset) {
    draw(page, reset); next = page.next; revision = stamp(page);
    sentinel.hidden = next == null;
  }
  async function load(offset, reset = false) {
    if (!active() || offset == null || (busy && !reset)) return;
    const mine = ++generation; busy = true;
    current?.abort(); const stop = new AbortController(); current=stop;
    const timeout = setTimeout(() => stop.abort(), 10000);
    try {
      const page = await request(offset, stop.signal);
      if (!active() || mine !== generation || !sentinel.isConnected) return;
      if (!reset && stamp(page) !== revision) { busy = false; await load(0, true); return; }
      apply(page, reset); observer.disconnect(); observer.observe(sentinel); error.hidden = true; error.textContent = "";
    } catch (failure) {
      if (active() && mine === generation) {error.hidden = false; error.textContent = "Could not load files or records. Use Refresh to try again.";}
    } finally { clearTimeout(timeout); if (mine === generation) busy = false; }
  }
  apply(first, true);
  const observer = new IntersectionObserver(entries => {
    if (!active() || !sentinel.isConnected) { observer.disconnect(); return; }
    if (entries.some(entry => entry.isIntersecting)) load(next);
  }, {root:document.getElementById("main"), rootMargin:`${2*(document.getElementById("main").clientHeight || innerHeight)}px`});
  observer.observe(sentinel);
  return {sentinel, error, invalidate:()=>{generation++;current?.abort();busy=false;}, refresh:iconButton("refresh", "Refresh", () => load(0, true)), reset:() => load(0, true), removed:() => {if (next != null) next=Math.max(0,next-1);}};
}
