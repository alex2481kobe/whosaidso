// The viewer's inline line icons, drawn with the current text colour on a 24-unit grid.
const NS = "http://www.w3.org/2000/svg";
const paths = {
  sun: "M12 8a4 4 0 1 0 0 8 4 4 0 0 0 0-8M12 2v2M12 20v2M2 12h2M20 12h2M5 5l1.5 1.5M17.5 17.5L19 19M5 19l1.5-1.5M17.5 6.5L19 5",
  moon: "M20 15A9 9 0 0 1 9 4a9 9 0 1 0 11 11Z",
  download: "M12 3v12M7 10l5 5 5-5M4 17v4h16v-4",
  down: "M6 9l6 6 6-6",
  copy: "M9 9h10v12H9zM5 15V3h10",
  open: "M14 4h6v6M20 4l-9 9M18 14v6H4V6h6",
  check: "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18zM8 12.5l2.7 2.7L16 9.8",
  search: "M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14zM20 20l-4-4",
  sort: "M8 4v16M4.5 7.5L8 4l3.5 3.5M16 20V4M12.5 16.5L16 20l3.5-3.5",
  retry: "M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6",
  close: "M6 6l12 12M18 6L6 18",
  file: "M6 3h8l4 4v14H6zM14 3v4h4M9 12h6M9 16h4",
};

// icon draws one line icon; className adds to "icon".
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
