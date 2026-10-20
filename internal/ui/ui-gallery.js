// Gallery presentation: artwork and titles, with provenance in artifact details.
// File availability never implies approval. Mutations retain the original server contract.
import { el, api, apiURL, send, project, when, stamp, icon, link, view, fullTitle } from "./ui.js";
import { chip, dropdown } from "./ui-parts.js";
let taskTitles = new Map();
const state = { project: "", search: "", kind: "", sort: "recent" };
const kinds = [["", "All types"], ["image", "Images"], ["video", "Videos"], ["text", "Text"], ["other", "Other"]];
const sorts = [["recent", "Newest first"], ["oldest", "Oldest first"], ["title", "Title A–Z"]];
const ordered = (entries) => state.sort === "oldest" ? entries.slice() : state.sort === "title" ? entries.slice().sort((a,b) => a.title.localeCompare(b.title)) : entries.slice().reverse();
const badge = (entry) => el("span", "verdict " + (entry.verdict || "unreviewed"), el("span", "verdict-dot"), entry.verdict === "approved" ? "Approved" : entry.verdict === "rejected" ? "Rejected" : "Unreviewed");
function action(glyph, label, fn, cls = "button") {
  const b = el("button", cls, icon(glyph), el("span", "", label));
   b.type = "button";
   b.addEventListener("click", fn);
   return b;
}
function preview(entry, src) {
  if (entry.status !== "present") return el("div", "gallery-none", icon("file"), el("strong", "", entry.status === "changed" ? "File changed" : entry.status === "missing" ? "File missing" : "File status UNKNOWN"), el("span", "", entry.status === "changed" ? "This file no longer matches its recorded version." : entry.status === "missing" ? "The recorded file is not available locally." : "File availability could not be determined."));
  if (entry.kind === "image") { const img = el("img");
   Object.assign(img, {src, alt:entry.title, loading:"lazy"});
   return el("div", "gallery-media", img);
   }
  if (entry.kind === "video") { const video = el("video");
   Object.assign(video, {src, controls:true, preload:"metadata", playsInline:true});
   return el("div", "gallery-media", video);
   }
  if (entry.kind === "text") {
    const pre = el("pre", "gallery-text", "Loading preview…");
   pre.tabIndex = 0; pre.setAttribute("aria-label", entry.title + " text preview");
    fetch(src).then(async r => { if (!r.ok) throw Error(`${r.status} ${r.statusText}`);
   const cut=r.headers.get("X-WhoSaidSo-Truncated");
   pre.textContent=(await r.text())+(cut?`\n[first ${Math.round(cut/1024)} KiB shown]`:"");
   }).catch(e=>{pre.textContent="Could not read file: "+e.message;
  });
   return pre;
  }
  return el("div", "gallery-none", icon("file"), "Preview unavailable");
}
function taskLinks(entry) {
  return (entry.tasks || []).map(t => t.project && t.project !== project() ? el("span", "artifact-task", `${t.project} / ${t.record_id}`) : link(t.record_id, taskTitles.get(t.record_id) || t.record_id, "artifact-task"));
}
function removeButton(entry, said, shownProject) {
  let armed = false, timer;
  const b=action("close", "Remove from gallery",()=>{
    if(!armed){armed=true;
  b.lastChild.textContent="Confirm removal";
  timer=setTimeout(()=>{armed=false;
  b.lastChild.textContent="Remove from gallery";
  },4000);
  return;
  }
    clearTimeout(timer);
  armed=false;
  b.lastChild.textContent="Remove from gallery";
    send("/api/gallery/remove",{project:shownProject,id:entry.id}).then(r=>{said.textContent=`Retraction captured. Admit ${r.captured} in the CLI to remove the entry. Your file is unchanged.`;
  }).catch(e=>{said.textContent=e.message;
  });
  },"button danger-button");
   return b;
}
// Reveal uses only the local viewer's authenticated endpoint, never a browser file API.
function revealButton(entry, said, shownProject) {
  const button=action("open","Reveal in Finder",async e=>{
    e.stopPropagation();
    if(button.disabled)return;
    button.disabled=true;
    said.textContent="";
    try { await send("/api/gallery/reveal",{project:shownProject,id:entry.id}); said.textContent="Reveal requested."; }
    catch(error) { said.textContent=error.message; }
    finally { button.disabled=false; }
  },"button artifact-reveal");
  button.setAttribute("aria-label","Reveal " + entry.title + " in Finder");
  return button;
}
function inspect(entry, src, trigger) {
  // A double click can follow the first click: keep a single viewer open.
  if(document.querySelector(".artifact-dialog"))return;
  const shownProject = project();
  const close=action("close","Close",()=>dialog.close());
  const said=el("p","gallery-said");
   said.setAttribute("role","status");
  const links=taskLinks(entry);
  const provenance=el("dl","artifact-facts",
    el("div","",el("dt","","Added by"),el("dd","",entry.added_by||"UNKNOWN")),
    el("div","",el("dt","","Added"),el("dd","",stamp(new Date(entry.added_at)))),
    el("div","",el("dt","","File status"),el("dd","",entry.status||"UNKNOWN")),
    el("div","",el("dt","","Location"),el("dd","mono",entry.path)));
  const review = el("details","artifact-technical",el("summary","","Review details"),
    el("p","",entry.verdict && entry.verdict !== "unreviewed" ? `${entry.verdict} by ${entry.verdict_by?.id || "UNKNOWN"}` : "No verdict recorded."),
    entry.verdict_note ? el("p","",entry.verdict_note) : null);
  const meta=el("aside","artifact-sidebar",badge(entry),
    entry.verdict && entry.verdict !== "unreviewed" && !entry.verdict_by?.id ? el("p","muted","Reviewer: UNKNOWN") : null,
    links.length ? el("div","artifact-section",el("h3","","Linked task"),el("div","artifact-task-list",links)) : null,
    entry.note ? el("p","artifact-description",entry.note) : null,
    review,
    el("details","artifact-technical",el("summary","","File details"),provenance,
      el("p","mono",entry.sha256?`SHA-256 ${entry.sha256}`:"Hash unknown"),el("p","",entry.length!=null?`${entry.length.toLocaleString()} bytes`:"Size unknown"),
      el("div","artifact-detail-actions",removeButton(entry,said,shownProject))),
    el("div","artifact-detail-actions",revealButton(entry,said,shownProject)),said);
  const content=preview(entry,src);
   const original=trigger.closest(".gallery-card")?.querySelector("video");
  const playbackTime = original?.currentTime || 0;
   original?.pause();
  const enlarged = content.querySelector?.("video");
  if(enlarged) enlarged.addEventListener("loadedmetadata",()=>{enlarged.currentTime=playbackTime;
  },{once:true});
  const dialog=el("dialog","artifact-dialog",el("div","artifact-dialog-head",el("strong","",entry.title),close),el("div","artifact-dialog-body",el("div","artifact-stage",content),meta));
  dialog.setAttribute("aria-label",entry.title);
   dialog.addEventListener("click",e=>{if(e.target===dialog)dialog.close();
  });
  const closeOnRoute=()=>dialog.close();
   window.addEventListener("hashchange",closeOnRoute);
  dialog.addEventListener("close",()=>{enlarged?.pause();
  if(original && enlarged?.readyState>=1) original.currentTime=enlarged.currentTime;
  window.removeEventListener("hashchange",closeOnRoute);
  dialog.remove();
  if(trigger.isConnected)trigger.focus();
  },{once:true});
  document.body.append(dialog);
  dialog.showModal();
  close.focus();
}
function card(entry) {
  const src=apiURL("/api/gallery/file",{project:project(),id:entry.id,cap:entry.cap});
  const media=preview(entry,src);
  const stage=el("div","artifact-preview",media);
  if(entry.kind !== "video") {
    stage.tabIndex=0;
    stage.setAttribute("role","button");
    stage.setAttribute("aria-label","Open " + entry.title);
    stage.addEventListener("click",()=>inspect(entry,src,stage));
    stage.addEventListener("keydown",e=>{if(e.target===stage && (e.key==="Enter" || e.key===" ")){e.preventDefault();inspect(entry,src,stage);}});
    // The full text remains keyboard-scrollable in the inspector.
    if(entry.kind === "text") media.removeAttribute("tabindex");
  }
  const title=el("button","artifact-title-button",entry.title);
  title.type="button";
  title.setAttribute("aria-label","Open " + entry.title + " details");
  title.addEventListener("click",()=>inspect(entry,src,title));
  const said=el("p","gallery-said");
  said.setAttribute("role","status");
  const reveal=revealButton(entry,said,project());
  const node=el("article","gallery-card",stage,el("div","gallery-card-body",el("h2","gallery-title",title),reveal,said));
  node.addEventListener("dblclick",e=>{
    if(e.target.closest(".artifact-reveal"))return;
    e.preventDefault();
    inspect(entry,src,title);
  });
  node.dataset.kind=entry.kind||"other";
  node.dataset.search=[entry.title,entry.note,entry.path,entry.added_by,entry.added_at,...(entry.tasks||[]).flatMap(t=>[t.record_id,taskTitles.get(t.record_id)||""])].join("\n").toLowerCase();
  return node;
}
// Legacy review query parameters are intentionally ignored: this is a display gallery.
export async function renderGallery() {
  const [answer, records] = await Promise.all([api("/api/gallery",{project:project()}), view("show").catch(() => ({records:[]}))]);
  taskTitles = new Map(records.records.map(r => [r.ref.record_id, fullTitle(r)]));
  if(state.project!==project())Object.assign(state,{project:project(),search:"",kind:""});
  const count=el("span","gallery-total");
  const empty=el("div","gallery-empty",icon("file"),el("h2","","No artifacts here"),el("p","","Try another filter, or ask your agent to add a file to the gallery."));
  const cards=el("div","gallery-cards");
  const tools=el("div","gallery-tools");
  let entries=answer.entries, drawn=new Map();
  const filter=()=>{let shown=0;
  for(const c of cards.children){c.hidden=!!((state.search&&!c.dataset.search.includes(state.search.toLowerCase()))||(state.kind&&c.dataset.kind!==state.kind));
  if(!c.hidden)shown++;
  }count.textContent=`${shown} ${shown===1?"artifact":"artifacts"}`;
  empty.hidden=shown>0;
  };
  const draw=()=>{const nodes=ordered(entries).map(e=>{const sig=JSON.stringify(e),old=drawn.get(e.id);
  if(old?.sig===sig)return old.node;
  const node=card(e);
  drawn.set(e.id,{sig,node});
  return node;
  });
  const keep=new Set(nodes);
  for(const c of cards.children)if(!keep.has(c))c.remove();
  let at=cards.firstChild;
  for(const node of nodes){if(node===at)at=at.nextSibling;
  else cards.insertBefore(node,at);
  }filter();
  };
  const paint=()=>{
  tools.replaceChildren(
    dropdown({prefix:"",options:kinds,value:state.kind,onPick:v=>{state.kind=v;paint();filter();}}),
    dropdown({glyph:"sort",prefix:"",options:sorts,value:state.sort,onPick:v=>{state.sort=v;paint();draw();}}));
  };
  const search=el("input","search-input");
  Object.assign(search,{type:"search",placeholder:"Search artifacts…",value:state.search,spellcheck:false});
  search.setAttribute("aria-label","Search artifacts");
  search.addEventListener("input",()=>{state.search=search.value.trim();
  filter();
  });
  const header=el("header","gallery-heading",el("h1","","Gallery"));
  paint();
  draw();
  const shown=project();
  let busy=false;
  const timer=setInterval(()=>{if(!cards.isConnected||project()!==shown){clearInterval(timer);
  return;
  }if(busy||document.visibilityState==="hidden")return;
  busy=true;
  const stop=new AbortController(),limit=setTimeout(()=>stop.abort(),10000);
  api("/api/gallery",{project:shown},stop.signal).then(next=>{if(cards.isConnected && JSON.stringify(entries)!==JSON.stringify(next.entries)){entries=next.entries;
  paint();
  draw();
  }}).catch(()=>{}).finally(()=>{clearTimeout(limit);
  busy=false;
  });
  },3000);
  return el("div","screen gallery-screen",header,el("div","gallery-toolbar",el("div","gallery-search-tools",el("label","search",icon("search"),search),tools)),cards,empty);
}
