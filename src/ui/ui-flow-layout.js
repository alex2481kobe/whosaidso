// Pure layout: boxes flow left to right, and an open box holds its own smaller flowchart.
const COLUMN = 120, ROW = 28, PAD = 22, TITLE = 40;

// Columns count steps from the start, so the chart fans out like a tree and every forward
// arrow reaches only the next column. Arrows that close a loop are left out of the count.
function columns(ids, links) {
  const out = new Map(ids.map(id => [id, []])), incoming = new Map(ids.map(id => [id, 0]));
  for (const [a, b] of links) { out.get(a).push(b); incoming.set(b, incoming.get(b) + 1); }
  const seen = new Map(), forward = new Map(ids.map(id => [id, []])), pointed = new Set();
  const visit = id => {
    seen.set(id, 1);
    for (const to of out.get(id)) {
      if (seen.get(to) === 1) continue;
      forward.get(id).push(to); pointed.add(to);
      if (!seen.has(to)) visit(to);
    }
    seen.set(id, 2);
  };
  // Starting from boxes nothing points at keeps every poll in the same arrangement.
  [...ids].sort((a, b) => incoming.get(a) - incoming.get(b) || ids.indexOf(a) - ids.indexOf(b)).forEach(id => { if (!seen.has(id)) visit(id); });
  const rank = new Map(ids.filter(id => !pointed.has(id)).map(id => [id, 0])), queue = [...rank.keys()];
  for (const id of queue) for (const to of forward.get(id)) if (!rank.has(to)) { rank.set(to, rank.get(id) + 1); queue.push(to); }
  const groups = [];
  for (const id of ids) (groups[rank.get(id)] ||= []).push(id);
  return groups.filter(Boolean);
}

// Two sweeps pull each box towards the boxes it connects to, which uncrosses most arrows.
function untangle(groups, links) {
  const place = new Map();
  const number = () => groups.forEach(group => group.forEach((id, i) => place.set(id, i - (group.length - 1) / 2)));
  const neighbours = id => links.flatMap(([a, b]) => a === id ? [b] : b === id ? [a] : []);
  for (let sweep = 0; sweep < 4; sweep++) {
    number();
    for (const group of sweep % 2 ? [...groups].reverse() : groups) {
      const weight = new Map(group.map(id => {
        const near = neighbours(id).map(n => place.get(n));
        return [id, near.length ? near.reduce((a, b) => a + b, 0) / near.length : place.get(id)];
      }));
      group.sort((a, b) => weight.get(a) - weight.get(b) || a.localeCompare(b));
    }
  }
}

function arrange(parent, graph) {
  const ids = graph.children(parent), inner = new Map();
  for (const id of ids) if (graph.isOpen(id)) inner.set(id, arrange(id, graph));
  const size = id => {
    const open = inner.get(id);
    return open ? {w: Math.max(open.w + 2 * PAD, graph.size(id).w), h: open.h + TITLE + PAD} : graph.size(id);
  };
  const links = graph.links(parent, ids), groups = columns(ids, links);
  untangle(groups, links);
  // A long column folds into several so unconnected boxes do not make a tower.
  const tallest = Math.max(5, Math.ceil(Math.sqrt(ids.length) * 1.3)), folded = [];
  for (const group of groups) for (let i = 0; i < group.length; i += tallest) folded.push(group.slice(i, i + tallest));
  const sizes = folded.map(group => group.map(size));
  const widths = sizes.map(row => Math.max(...row.map(s => s.w)));
  const heights = sizes.map(row => row.reduce((h, s) => h + s.h, 0) + (row.length - 1) * ROW);
  const height = Math.max(0, ...heights), items = [];
  let x = 0;
  folded.forEach((group, c) => {
    let y = (height - heights[c]) / 2;
    group.forEach((id, r) => {
      const s = sizes[c][r];
      items.push({id, x: x + (widths[c] - s.w) / 2, y, w: s.w, h: s.h, inner: inner.get(id)});
      y += s.h + ROW;
    });
    x += widths[c] + COLUMN;
  });
  return {w: Math.max(0, x - COLUMN), h: height, items};
}

export function layout(graph) {
  const rects = new Map();
  const flatten = (block, dx, dy, depth) => {
    for (const item of block.items) {
      const x = dx + item.x, y = dy + item.y;
      rects.set(item.id, {x, y, w: item.w, h: item.h, open: !!item.inner, depth});
      if (item.inner) flatten(item.inner, x + (item.w - item.inner.w) / 2, y + TITLE, depth + 1);
    }
  };
  flatten(arrange("", graph), 0, 0, 0);
  return rects;
}

// Arrows leave and enter a box at spread points, ordered by where the other end sits.
export function routes(edges, rects) {
  const ends = new Map(), routed = edges.flatMap(e => {
    const a = rects.get(e.from), b = rects.get(e.to);
    if (!a || !b) return [];
    const forward = a.x + a.w <= b.x, backward = b.x + b.w <= a.x;
    const sides = forward ? ["right", "left"] : backward ? ["left", "right"] : ["right", "right"];
    return [{e, a, b, sides, start: 0, end: 0}];
  });
  const claim = (key, route, field, other) => { if (!ends.has(key)) ends.set(key, []); ends.get(key).push({route, field, other}); };
  for (const r of routed) {
    claim(r.e.from + "\n" + r.sides[0], r, "start", r.b.y + r.b.h / 2);
    claim(r.e.to + "\n" + r.sides[1], r, "end", r.a.y + r.a.h / 2);
  }
  for (const [key, list] of ends) {
    const box = rects.get(key.split("\n")[0]), spread = Math.min(12, (box.h - 20) / Math.max(1, list.length - 1));
    list.sort((p, q) => p.other - q.other).forEach((p, i) => { p.route[p.field] = (i - (list.length - 1) / 2) * spread; });
  }
  const solid = [...rects.values()].filter(r => !r.open);
  return routed.map(({e, a, b, sides, start, end}, index) => {
    const x1 = sides[0] === "right" ? a.x + a.w : a.x, y1 = a.y + a.h / 2 + start;
    const x2 = sides[1] === "right" ? b.x + b.w : b.x, y2 = b.y + b.h / 2 + end;
    const out = sides[0] === "right" ? 1 : -1, back = sides[1] === "right" ? 1 : -1;
    const bend = Math.max(44, Math.min(120, Math.abs(x2 - x1) / 2));
    const c1 = x1 + out * bend, c2 = x2 + back * bend;
    // A long arrow arcs over the boxes between its ends instead of running behind them.
    const low = Math.min(x1, x2), high = Math.max(x1, x2), top = Math.min(y1, y2) - 12, bottom = Math.max(y1, y2) + 12;
    const between = solid.filter(r => r !== a && r !== b && r.x >= low && r.x + r.w <= high && r.y < bottom && r.y + r.h > top);
    const lift = between.length ? (index % 2 ? 1 : -1) * (Math.max(...between.map(r => r.h)) * 1.1 + 22 + (index % 4) * 11) : 0;
    return {...e, d: `M ${x1} ${y1} C ${c1} ${y1 + lift} ${c2} ${y2 + lift} ${x2} ${y2}`, mid: [(x1 + 3 * c1 + 3 * c2 + x2) / 8, (y1 + y2) / 2 + .75 * lift]};
  });
}
