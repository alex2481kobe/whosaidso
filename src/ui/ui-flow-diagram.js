const NS = "http://www.w3.org/2000/svg";
const svg = (tag, attrs = {}, text) => {
  const n = document.createElementNS(NS,tag);
  for (const [k,v] of Object.entries(attrs)) n.setAttribute(k,v);
  if (text != null) n.textContent = text;
  return n;
};
export const attention = {planned:"not built yet",authored:"not checked",drifted:"changed",missing:"missing",unplanned:"not in plan",unknown:"not checked",ambiguous:"not checked",broken:"not checked"};
const ctx = document.createElement("canvas").getContext("2d");
const measure = (text,size=16) => {ctx.font=`550 ${size}px system-ui`; return ctx.measureText(text).width;};
function wrap(text,width,size=16) {
  const rows=[]; let row="";
  for (const word of String(text).split(/\s+/)) {
    if (row && measure(row+" "+word,size)>width) {rows.push(row);row="";}
    for (const c of (row ? " " : "")+word) {if (measure(row+c,size)>width) {rows.push(row);row="";} row+=c;}
  }
  if (row) rows.push(row); return rows;
}
function connections(edges,undirected=false) {
  const pairs=new Map();
  for(const e of edges){const key=JSON.stringify(undirected?[e.from,e.to].sort():[e.from,e.to]);
    if(!pairs.has(key))pairs.set(key,{...e,key:`pair:${key}`,extra:e.extra||0});
    else {const p=pairs.get(key);p.extra+=(e.extra||0)+1;p.reverse ||= e.reverse||e.from!==p.from;if(!p.when)p.when=e.when;}
  }
  return [...pairs.values()];
}
function layout(nodes,edges,width) {
  const byId=new Map(nodes.map(n=>[n.id,n])), mark=new Map(),order=[],forward=[],back=new Set();
  const visit=id=>{mark.set(id,1);for(const e of edges.filter(e=>e.from===id&&byId.has(e.to))){if(mark.get(e.to)===1){back.add(e.key);continue;}forward.push(e);if(!mark.has(e.to))visit(e.to);}mark.set(id,2);order.unshift(id);};
  nodes.forEach(n=>{if(!mark.has(n.id))visit(n.id);});
  if(back.size && edges.some(e=>back.has(e.key)&&e.from!==e.to) || Math.max(edges.length,nodes.reduce((sum,n)=>sum+(n.type==="function"?n.children||0:0),0))>1.3*nodes.length)return grid(nodes,edges,width);
  const rank=new Map(nodes.map(n=>[n.id,0]));
  for(const id of order)for(const e of forward.filter(e=>e.from===id))rank.set(e.to,Math.max(rank.get(e.to),rank.get(id)+1));
  const units=nodes.map(n=>{
    const returns=edges.filter(e=>e.from===n.id&&back.has(e.key)).map(e=>({...e,rows:wrap(`↩ ${e.when ? e.when+" · " : ""}${byId.get(e.to).name}`,132,11)}));
    returns.forEach(e=>{e.h=Math.max(24,e.rows.length*14+8);});
    const rw=Math.max(0,...returns.map(e=>Math.max(...e.rows.map(s=>measure(s,11)))+16)),rh=returns.reduce((h,e)=>h+e.h,0);
    const w=Math.min(width-48,Math.max(112,Math.min(168,measure(n.name)+36))),rows=wrap(n.name,w-36),h=56+(rows.length-1)*20+(attention[n.state]?18:0);
    const below=w+12+rw>width-48||rh>h;
    return {n,w,h,rows,returns,rw,rh,below,uw:below?Math.max(w,rw):w+(rw?12+rw:0),uh:below?h+(rh?12+rh:0):Math.max(h,rh)};
  });
  const levels=[...new Set(rank.values())].sort((a,b)=>a-b).map(r=>units.filter(u=>rank.get(u.n.id)===r));
  const labels=new Map(forward.map(e=>[e.key,e.when?wrap(e.when,112,11):e.extra?[""]:[]])),out=new Map(),incoming=new Map();
  for(const e of forward){const h=labels.get(e.key).length?labels.get(e.key).length*14+8:0;out.set(e.from,(out.get(e.from)||0)+h);incoming.set(e.to,(incoming.get(e.to)||0)+h);}
  const widths=levels.map(group=>Math.max(...group.map(u=>u.uw))),need=widths.reduce((a,b)=>a+b,0)+32*(levels.length-1)+48;
  const horizontal=levels.length>1&&need<=width,points=new Map(),gap=Math.max(56,Math.max(0,...incoming.values())+28);
  let height=0;
  const place=(u,x,y,row,floor)=>{const bx=x+(u.below?(u.uw-u.w)/2:0),by=y+(!u.below?(u.uh-u.h)/2:0);points.set(u.n.id,{...u,x:bx,y:by,left:x,bottom:y+u.uh,row,floor,rx:u.below?x+(u.uw-u.rw)/2:bx+u.w+12,ry:u.below?by+u.h+12:y+(u.uh-u.rh)/2});};
  const band=horizontal?Math.max(0,...out.values())+16:0;
  if(horizontal){
    height=Math.max(...levels.map(g=>g.reduce((h,u)=>h+u.uh,0)+(g.length-1)*28))+band+48;
    let x=24;const spacing=(width-48-widths.reduce((a,b)=>a+b,0))/(levels.length-1);
    levels.forEach((group,i)=>{let y=band+24+(height-band-48-group.reduce((h,u)=>h+u.uh,0)-(group.length-1)*28)/2;group.forEach(u=>{place(u,x,y,i,y+u.uh);y+=u.uh+28;});x+=widths[i]+spacing;});
  }else{
    let y=24,row=0;
    for(const group of levels){let chunk=[],used=0;const flush=()=>{if(!chunk.length)return;const h=Math.max(...chunk.map(u=>u.uh));let x=(width-used)/2;for(const u of chunk){place(u,x,y,row,y+h);x+=u.uw+28;}y+=h+gap;row++;chunk=[];used=0;};for(const u of group){if(used+u.uw+(chunk.length?28:0)>width-48)flush();used+=u.uw+(chunk.length?28:0);chunk.push(u);}flush();}
    height=Math.max(120,y-gap+24);
  }
  const labelOffsets=new Map(),routes=[],stems=new Map();
  for(const e of forward){
    const a=points.get(e.from),b=points.get(e.to);let stem,path;
    if(horizontal){const j=a.left+a.uw+(width-48-widths.reduce((a,b)=>a+b,0))/(levels.length-1)/2,sy=a.y+a.h/2,ty=b.y+b.h/2;
      stem=a.below&&a.returns.length?[[a.x,sy],[a.left-12,sy],[a.left-12,a.bottom+12],[j,a.bottom+12],[j,sy]]:a.returns.length?[[a.x+a.w/2,a.y+a.h],[a.x+a.w/2,a.bottom+12],[j,a.bottom+12],[j,sy]]:[[a.x+a.w,sy],[j,sy]];
      path=b.row===a.row+1?[[j,sy],[j,ty],[b.x,ty]]:[[j,sy],[j,band+8],[b.x-16,band+8],[b.x-16,ty],[b.x,ty]];
    }else{const j=a.floor+gap/2,c=a.x+a.w/2,t=b.x+b.w/2;
      stem=a.below&&a.returns.length?[[a.x,a.y+a.h/2],[a.left-12,a.y+a.h/2],[a.left-12,j],[c,j]]:[[c,a.y+a.h],[c,j]];
      path=b.row===a.row+1?[[c,j],[t,j],[t,b.y]]:[[c,j],[width-12,j],[width-12,b.y-gap/2],[t,b.y-gap/2],[t,b.y]];
    }
    const lines=labels.get(e.key),lh=lines.length*14+8,key=horizontal?e.from:e.to,offset=labelOffsets.get(key)||0;labelOffsets.set(key,offset+(lines.length?lh:0));
    const cx=horizontal?(a.left+a.uw+b.x)/2:b.x+b.w/2,cy=horizontal?8+offset+lh/2:b.y-gap+12+offset+lh/2;
    const lw=Math.max(0,...lines.map(s=>measure(s,11)))+12+(e.extra?22:0);
    let label=lines.length?{x:Math.max(8,Math.min(width-lw-8,cx-lw/2)),y:cy-lh/2,w:lw,h:lh,rows:lines,extra:e.extra}:null;
    if(horizontal&&label){
      const mid=(a.left+a.uw+b.x)/2,ty=b.y+b.h/2,j=a.left+a.uw+(width-48-widths.reduce((a,b)=>a+b,0))/(levels.length-1)/2;
      const obstacles=[...points.values()].flatMap(p=>[{x:p.x,y:p.y,w:p.w,h:p.h},...(p.returns.length?[{x:p.rx,y:p.ry,w:p.rw,h:p.rh}]:[])]).concat(routes.flatMap(e=>e.label?[e.label]:[]));
      const safeY=ty<a.y+a.h/2?Math.min(ty,a.y-lh/2-8):Math.max(ty,a.y+a.h+lh/2+8);
      for(const [x,y]of [[mid,ty],[mid,a.y-lh/2-8],[mid,a.y+a.h+lh/2+8],[j-7-lw/2,ty],[j-7-lw/2,safeY],[cx,cy]]){
        const r={...label,x:Math.max(8,Math.min(width-lw-8,x-lw/2)),y:y-lh/2};
        if(r.y>=8&&!obstacles.some(b=>r.x<b.x+b.w+3&&r.x+r.w>b.x-3&&r.y<b.y+b.h+3&&r.y+r.h>b.y-3)){label=r;break;}
      }
    }
    if(!stems.has(e.from))stems.set(e.from,{key:`stem:${e.from}`,from:e.from,path:stem});
    routes.push({...e,path,label});
  }
  const stemList=[...stems.values()],allPaths=[...routes,...stemList];
  if(horizontal){const lift=Math.min(...[...points.values()].map(p=>p.y-8),...routes.flatMap(e=>e.label?[e.label.y]:[]),...allPaths.flatMap(e=>e.path.map(p=>p[1])))-16;for(const p of points.values()){p.y-=lift;p.bottom-=lift;p.floor-=lift;p.ry-=lift;}for(const e of allPaths){e.path=e.path.map(([x,y])=>[x,y-lift]);if(e.label)e.label.y-=lift;}height=Math.max(...[...points.values()].map(p=>p.bottom),...routes.flatMap(e=>e.label?[e.label.y+e.label.h]:[]))+24;}
  return {points,routes,stems:stemList,back,height,horizontal};
}
// Architecture layers use stable nearby cells; only the inspected box's connections are shown.
function grid(nodes,edges,width) {
  const cols=Math.min(Math.ceil(Math.sqrt(nodes.length)),Math.max(1,Math.floor((width-48+64)/232))),gapX=64,w=(width-48-(cols-1)*gapX)/cols;
  const routes=connections(edges,true).map(e=>({...e,rows:wrap(e.when||"",Math.min(112,w),11)}));
  const degree=Math.max(1,...nodes.map(n=>routes.filter(e=>(e.rows.length||e.extra)&&(e.from===n.id||e.to===n.id)).length)),tiers=Math.ceil(degree/cols);
  const gapY=Math.max(64,tiers*(Math.max(22,...routes.map(e=>e.rows.length*14+8))+4)+16),ordered=[...nodes].sort((a,b)=>a.id.localeCompare(b.id));
  const distance=(a,b)=>Math.abs(a%cols-b%cols)+Math.abs(Math.floor(a/cols)-Math.floor(b/cols));
  const cost=()=>{const index=new Map(ordered.map((n,i)=>[n.id,i]));return routes.reduce((sum,e)=>sum+distance(index.get(e.from),index.get(e.to)),0);};
  // Strict improvements and identity tie breaks keep every poll in the same arrangement.
  for(let pass=0;pass<3;pass++)for(let a=0;a<ordered.length;a++)for(let b=a+1;b<ordered.length;b++){const before=cost();[ordered[a],ordered[b]]=[ordered[b],ordered[a]];if(cost()>=before)[ordered[a],ordered[b]]=[ordered[b],ordered[a]];}
  const units=ordered.map(n=>{const rows=wrap(n.name,w-36);return {n,w,rows,h:56+(rows.length-1)*20+(attention[n.state]?18:0),returns:[]};}),points=new Map(),heights=[];
  let y=gapY;
  for(let i=0;i<units.length;i+=cols){const group=units.slice(i,i+cols),h=Math.max(...group.map(u=>u.h));heights.push({top:y,bottom:y+h});group.forEach((u,j)=>points.set(u.n.id,{...u,x:24+j*(w+gapX),y:y+(h-u.h)/2,row:heights.length-1}));y+=h+gapY;}
  const placed=[];
  for(const e of routes){
    const a=points.get(e.from),b=points.get(e.to),above=b.row<=a.row,cy=above?heights[a.row].top-gapY/2:heights[a.row].bottom+gapY/2,ax=a.x+a.w/2,bx=b.x+b.w/2;
    if(a.row===b.row){e.path=[[ax,a.y],[ax,cy],[bx,cy],[bx,b.y]];if(a===b)e.path=[[a.x+a.w,a.y+a.h/2],[a.x+a.w+16,a.y+a.h/2],[a.x+a.w+16,cy],[ax,cy],[ax,a.y]];}
    else {const left=bx>=ax,gx=left?Math.max(12,b.x-gapX/2):Math.min(width-12,b.x+b.w+gapX/2);e.path=[[ax,above?a.y:a.y+a.h],[ax,cy],[gx,cy],[gx,b.y+b.h/2],[left?b.x:b.x+b.w,b.y+b.h/2]];}
    e.label=null;
    if(e.rows.length||e.extra){const rows=e.rows.length?e.rows:[""],lw=Math.max(...rows.map(s=>measure(s,11)))+12+(e.extra?22:0),lh=rows.length*14+8;
      const horizontal=e.path.filter(p=>p[1]===cy).map(p=>p[0]),left=Math.min(...horizontal),right=Math.max(...horizontal),preferred=(left+right)/2,candidates=[preferred,...Array.from({length:9},(_,i)=>left+i*(right-left)/8)];
      const related=p=>[e.from,e.to].some(id=>id===p.from||id===p.to);
      let x=Math.max(12,Math.min(width-lw-12,preferred-lw/2)),labelY=cy;
      search:for(let lane=0;lane<tiers;lane++)for(const cx of candidates){const next=Math.max(12,Math.min(width-lw-12,cx-lw/2)),y=cy-gapY/2+8+(lane+.5)*(gapY-16)/tiers;
        if(!placed.some(p=>related(p)&&y-lh/2<p.label.y+p.label.h+3&&y+lh/2>p.label.y-3&&next<p.label.x+p.label.w+4&&next+lw>p.label.x-4)){x=next;labelY=y;break search;}}
      e.path=e.path.map(([x,y])=>[x,y===cy?labelY:y]);e.label={x,y:labelY-lh/2,w:lw,h:lh,rows,extra:e.extra};placed.push(e);
    }
  }
  return {points,routes,stems:[],back:new Set(),height:y,dense:true,horizontal:false};
}
// Split strokes at label backgrounds, then round only the remaining visible bends.
function stroke(points,labels) {
  const pieces=[];let piece=[];
  for(let i=1;i<points.length;i++){
    const a=points[i-1],b=points[i],dx=b[0]-a[0],dy=b[1]-a[1],length=Math.abs(dx||dy);if(!length)continue;
    let intervals=[[0,1]];
    for(const r of labels){const cross=dx?(a[1]>=r.y-2&&a[1]<=r.y+r.h+2):(a[0]>=r.x-2&&a[0]<=r.x+r.w+2);if(!cross)continue;
      const t=[((dx?r.x-2:r.y-2)-(dx?a[0]:a[1]))/(dx||dy),((dx?r.x+r.w+2:r.y+r.h+2)-(dx?a[0]:a[1]))/(dx||dy)].sort((a,b)=>a-b);
      intervals=intervals.flatMap(([l,h])=>t[1]<=l||t[0]>=h?[[l,h]]:[[l,Math.min(h,t[0])],[Math.max(l,t[1]),h]].filter(([x,y])=>y>x));
    }
    for(const [l,h]of intervals){const start=[a[0]+dx*l,a[1]+dy*l],end=[a[0]+dx*h,a[1]+dy*h];if(piece.length&&piece.at(-1).some((v,j)=>Math.abs(v-start[j])>.01)){pieces.push(piece);piece=[];}if(!piece.length)piece.push(start);piece.push(end);}
  }
  if(piece.length)pieces.push(piece);
  return pieces.map(p=>{let d=`M ${p[0].join(" ")}`;for(let i=1;i<p.length-1;i++){const a=p[i-1],b=p[i],c=p[i+1],r=Math.min(6,Math.hypot(b[0]-a[0],b[1]-a[1])/2,Math.hypot(c[0]-b[0],c[1]-b[1])/2);const near=(v)=>b.map((n,j)=>n+(v[j]-n)*r/(Math.hypot(v[0]-b[0],v[1]-b[1])||1));d+=` L ${near(a).join(" ")} Q ${b.join(" ")} ${near(c).join(" ")}`;}return {d:d+` L ${p.at(-1).join(" ")}`,end:p.at(-1)};});
}
export function diagram(onGo,onDetails,onSelect=()=>{}) {
  const root=svg("svg",{class:"flow-diagram",role:"group","aria-label":"Flowchart"}),defs=svg("defs"),lines=svg("g",{class:"flow-arrows"}),boxes=svg("g",{class:"flow-boxes"});
  for(const active of [false,true]){const m=svg("marker",{id:active?"flow-arrow-active":"flow-arrow",viewBox:"0 0 10 10",refX:9,refY:5,markerWidth:5,markerHeight:5,orient:"auto-start-reverse"});m.append(svg("path",{class:active?"flow-head active":"flow-head",d:"M 2 2 L 8 5 L 2 8"}));defs.append(m);}root.append(defs,lines,boxes);
  const items=new Map(),arrows=new Map();let latest,selected="",hovered="",shape="",plan;
  const highlight=()=>{const focus=hovered||selected,connected=new Set([focus]);const visible=plan?.dense?plan.routes.filter(e=>focus&&(e.from===focus||e.to===focus)):[],labels=visible.flatMap(e=>e.label?[e.label]:[]);if(plan?.dense&&focus)for(const e of plan.routes)if(e.from===focus||e.to===focus){connected.add(e.from);connected.add(e.to);}
    for(const [id,item]of items){item.g.classList.toggle("selected",id===selected);item.g.classList.toggle("dimmed",!!(plan?.dense&&focus&&!connected.has(id)));}
    for(const item of arrows.values()){const active=plan?.dense?!!focus&&(item.from===focus||item.to===focus):[selected,hovered].some(id=>id&&(item.from===id||item.to===id||item.targets?.includes(id)));item.g.classList.toggle("active",active);item.g.style.display=plan?.dense&&!active?"none":"";
      if(plan?.dense&&active){const e=item.route;item.heads=[];const parts=stroke(e.path,labels).map(p=>{const n=svg("path",{d:p.d});if(p.end.every((v,j)=>Math.abs(v-e.path.at(-1)[j])<.01))item.heads.push(n);return n;});if(e.reverse&&parts.length)parts[0].setAttribute("marker-start","url(#flow-arrow-active)");item.g.replaceChildren(...parts,...(item.caption?[item.caption]:[]));}
      for(const path of item.heads||[])path.setAttribute("marker-end",active?"url(#flow-arrow-active)":"url(#flow-arrow)");}
  };
  const choose=id=>{selected=id;highlight();onSelect(id);items.get(id)?.a.focus({preventScroll:true});items.get(id)?.g.scrollIntoView({block:"nearest",inline:"nearest"});};
  const redraw=()=>{
    if(!latest)return;const width=Math.max(180,root.parentElement?.clientWidth||960),nodes=latest.nodes||[],edges=connections((latest.steps||[]).flatMap((s,i)=>(Array.isArray(s.to)?s.to:[s.to]).map((to,j)=>({...s,to,key:`edge:${s.id||i}:${j}`}))).filter(e=>nodes.some(n=>n.id===e.from)&&nodes.some(n=>n.id===e.to)));
    const signature=JSON.stringify([width,nodes.map(n=>[n.id,n.name,!!attention[n.state],n.type,n.type==="function"?n.children:0]),edges.map(e=>[e.key,e.from,e.to,e.when,e.extra])]),moved=signature!==shape;
    if(moved){plan=layout(nodes,edges,width);shape=signature;root.setAttribute("viewBox",`0 0 ${width} ${plan.height}`);root.style.height=`${plan.height}px`;root.dataset.direction=plan.dense?"grid":plan.horizontal?"horizontal":"vertical";}
    const keep=new Set();
    for(const n of nodes){keep.add(n.id);let item=items.get(n.id);
      if(!item){const g=svg("g",{class:"flow-box","data-node":n.id}),a=svg("a"),rect=svg("rect",{rx:12}),title=svg("text",{class:"flow-name","text-anchor":"middle"}),state=svg("text",{class:"flow-word","text-anchor":"middle"}),deeper=svg("text",{class:"flow-deeper"},"›"),info=svg("g",{class:"flow-info",role:"button",tabindex:0});info.append(svg("rect",{width:24,height:20,rx:6}),svg("text",{x:12,y:13,"text-anchor":"middle"},"..."));a.append(rect,title,state,deeper);g.append(a,info);boxes.append(g);item={g,a,rect,title,state,deeper,info};items.set(n.id,item);
        a.addEventListener("click",e=>{e.preventDefault();onGo(item.data);});g.addEventListener("mouseenter",()=>{hovered=n.id;highlight();});g.addEventListener("mouseleave",()=>{hovered="";highlight();});g.addEventListener("focusin",()=>{hovered=n.id;highlight();});g.addEventListener("focusout",e=>{if(!g.contains(e.relatedTarget)){hovered="";highlight();}});const detail=e=>{e.preventDefault();e.stopPropagation();selected=n.id;highlight();onDetails(item.data);};info.addEventListener("click",detail);info.addEventListener("keydown",e=>{if(["Enter"," "].includes(e.key))detail(e);});
      }
      const p=plan.points.get(n.id);item.data=n;item.g.setAttribute("transform",`translate(${p.x} ${p.y})`);item.g.dataset.state=n.state;item.a.setAttribute("href",onGo(n,true));item.a.setAttribute("aria-label",`Open ${n.name}`);item.rect.setAttribute("width",p.w);item.rect.setAttribute("height",p.h);
      if(moved){const y=(p.h-p.rows.length*20-(attention[n.state]?18:0))/2+15;item.title.replaceChildren(...p.rows.map((s,i)=>svg("tspan",{x:p.w/2,y:y+i*20},s)));item.state.setAttribute("x",p.w/2);item.state.setAttribute("y",y+p.rows.length*20);}
      item.state.textContent=attention[n.state]||"";item.deeper.setAttribute("x",p.w-14);item.deeper.setAttribute("y",p.h/2+5);item.deeper.style.display=n.children?"":"none";item.info.setAttribute("transform",`translate(${p.w-20} -8)`);item.info.setAttribute("aria-label",`Details for ${n.name}`);
    }
    for(const [id,item]of items)if(!keep.has(id)){item.g.remove();items.delete(id);}
    const used=new Set(),labels=plan.routes.flatMap(e=>e.label?[e.label]:[]);
    const entry=(key,from,to,kind)=>{used.add(key);let item=arrows.get(key);if(!item){const g=svg("g");lines.append(g);item={g};arrows.set(key,item);}Object.assign(item,{from,to,targets:(kind==="stem"?plan.routes:kind==="return"?edges.filter(e=>plan.back.has(e.key)):[]).filter(e=>e.from===from).map(e=>e.to)});item.g.dataset.kind=kind;item.g.dataset.from=from;if(to)item.g.dataset.to=to;return item;};
    const paths=(item,points,head)=>{item.heads=[];return stroke(points,labels).map(p=>{const n=svg("path",{d:p.d});if(head&&p.end.every((v,j)=>Math.abs(v-points.at(-1)[j])<.01))item.heads.push(n);return n;});};
    for(const e of [...plan.stems,...plan.routes]){const item=entry(e.key,e.from,e.to,e.label===undefined?"stem":"forward");if(moved){item.route=e;item.caption=null;const children=paths(item,e.path,e.label!==undefined);if(e.reverse&&children.length)children[0].setAttribute("marker-start","url(#flow-arrow-active)");if(e.label){const r=e.label,g=svg("g",{class:"flow-label",transform:`translate(${r.x} ${r.y})`});g.append(svg("rect",{width:r.w,height:r.h,rx:5}),svg("text",{"text-anchor":"middle"}));g.children[1].append(...r.rows.map((s,i)=>svg("tspan",{x:(r.w-(r.extra?22:0))/2,y:14+i*14},s)));if(r.extra)g.children[1].append(svg("tspan",{x:r.w-14,y:14,class:"flow-more"},`+${r.extra}`));item.caption=g;children.push(g);}item.g.replaceChildren(...children);}}
    for(const p of plan.points.values())if(p.returns.length){
      if(p.below){const stem=entry(`return:${p.n.id}`,p.n.id,null,"return");if(moved)stem.g.replaceChildren(svg("path",{d:`M ${p.x+p.w/2} ${p.y+p.h} L ${p.x+p.w/2} ${p.ry}`}));}
      let y=p.ry;for(const e of p.returns){const item=entry(e.key,e.from,e.to,"return-link");if(moved){item.heads=[];const a=svg("a",{class:"flow-return",href:"#",role:"button",tabindex:0,transform:`translate(${p.rx} ${y})`,"aria-label":`Select ${nodes.find(n=>n.id===e.to).name}`});a.append(svg("rect",{width:p.rw,height:e.h,rx:8}),svg("text",{"text-anchor":"middle"}));a.children[1].append(...e.rows.map((s,i)=>svg("tspan",{x:p.rw/2,y:15+i*14},s)));const select=event=>{event.preventDefault();choose(e.to);};a.addEventListener("click",select);a.addEventListener("keydown",event=>{if(event.key===" ")select(event);});item.g.replaceChildren(...(p.below?[]:[svg("path",{d:`M ${p.x+p.w} ${y+e.h/2} L ${p.rx} ${y+e.h/2}`})]),a);}y+=e.h;}
    }
    for(const [key,item]of arrows)if(!used.has(key)){item.g.remove();arrows.delete(key);}highlight();
  };
  const observer=new ResizeObserver(redraw);
  return {node:root,update(data,pick=""){latest=data;selected=pick;redraw();if(root.isConnected)observer.observe(root.parentElement);},disconnect(){observer.disconnect();}};
}
