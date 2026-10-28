// Execute the actual embedded ES modules against synthetic HTTP answers.
// Assertions cover bounded startup and gallery interactions, not browser paint.
import vm from 'node:vm';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import assert from 'node:assert/strict';
import {Node,flatten} from './dom.mjs';
const body=new Node('body'),ids=new Map();
const doc=new Node('document');Object.assign(doc,{body,visibilityState:'visible',documentElement:new Node('html'),activeElement:null});
const create=tag=>{const n=new Node(tag);n.onFocus=node=>{doc.activeElement=node;};return n;};
doc.createElement=create;doc.createElementNS=(_,tag)=>create(tag);doc.createTextNode=text=>new Node('#text',text);
doc.getElementById=id=>{if(!ids.has(id)){const n=create(id==='main'?'main':id==='project-button'||id==='theme-toggle'?'button':'div');n.id=id;n.hidden=id==='project-menu';body.append(n);ids.set(id,n);}return ids.get(id);};
doc.querySelector=s=>body.querySelector(s);doc.querySelectorAll=s=>body.querySelectorAll(s);
const win=new Node('window');Object.assign(win,{scrollY:0,scrollTo(){}});
const timers=new Map(),intervals=new Map();let serial=0;
const requests=[];let hold,release,delayGallery=null,fail=false,copied='',recordSequence=1,timeSequence=1,textFail=true,refuseDelete=false;
const entry=i=>({id:String(i).padStart(26,'0'),title:'Artifact '+i,kind:'image',status:'present',path:'shot-'+i+'.png',sha256:'a'.repeat(64),length:20000,added_at:'2026-01-01T00:00:00Z',tasks:[],cap:'fixture',verdict:'unreviewed'});
const entries=Array.from({length:60},(_,i)=>entry(i));entries[3].status='unknown';entries[3].status_reason='not compared in list';entries[4].status='missing';entries[4].path='';entries[4].status_reason='open unavailable-file: no such file or directory';entries[5].kind='text';entries[5].path='shot-5.txt';entries[6].kind='video';entries[6].path='shot-6.mp4';entries[7].kind='';entries[7].path='shot-7.pdf';
const gallery=(offset,kind='')=>{const selected=kind?entries.filter(e=>(e.kind||'other')===kind):entries;return {revision:entries.map(e=>e.id).join('|'),entries:selected.slice(offset,offset+24),offset,total:selected.length,next:offset+24<selected.length?offset+24:null,task_titles:{}};};
hold=new Promise(resolve=>{release=resolve;});
const fetch=async (raw,options={})=>{
  const url=new URL(raw,'http://viewer');requests.push(url);
  if(url.pathname==='/api/gallery/file' && url.searchParams.get('id')===entries[5].id)return textFail?{ok:false,status:503,statusText:'Unavailable'}:{ok:true,headers:{get(){return null;}},text:async()=> 'synthetic text'};
  if(url.pathname==='/api/gallery/reveal')return {ok:true,json:async()=>({revealed:url.searchParams.get('id')})};
  if(url.pathname==='/api/gallery/remove'){if(refuseDelete)return {ok:false,status:409,json:async()=>({error:{message:'Review required'}})};const id=url.searchParams.get('id');entries.splice(entries.findIndex(e=>e.id===id),1);return {ok:true,json:async()=>({removed:id})};}
  if(url.pathname==='/api/projects')return {ok:true,json:async()=>({current:'test/viewer',projects:[{id:'test/viewer',available:true}]})};
  if(url.pathname==='/api/gallery'){
    if(hold){await hold;hold=null;}
    if(delayGallery){const pending=delayGallery;delayGallery=null;await pending;}
    if(fail)return {ok:false,status:500,json:async()=>({error:{message:'fixture refresh failure'}})};
    return {ok:true,json:async()=>gallery(Number(url.searchParams.get('offset')),url.searchParams.get('kind'))};
  }
  if(url.pathname==='/api/screen' && url.searchParams.get('screen')==='status')return {ok:true,json:async()=>({watermark:{sequence:recordSequence,head:{command_id:String(recordSequence).padStart(26,'0')}}})};
  if(url.pathname==='/api/view' && url.searchParams.get('id'))return {ok:true,json:async()=>url.searchParams.get('view')==='history'?{events:[],watermark:{sequence:recordSequence,head:{command_id:String(recordSequence).padStart(26,'0')}}}:{result:'UNKNOWN',reason:'synthetic absent record',records:[],sources:[]}};
  if(url.pathname==='/api/screen') {
    const allowed=new Set(['project','screen','offset','search','kind','status','sort','status_counts','under']);
    for(const key of url.searchParams.keys()) if(!allowed.has(key)) return {ok:false,status:400,json:async()=>({error:{message:'invalid screen parameter '+key}})};
  }
  if(url.pathname==='/api/screen' && url.searchParams.get('screen')==='tasks'){
    const offset=Number(url.searchParams.get('offset')||0);
    if(url.searchParams.get('under')===String(999).padStart(26,'0'))return {ok:true,json:async()=>({result:'UNKNOWN',reason:'Task unavailable at this watermark',rows:[],counts:{open:30},next:null})};
    return {ok:true,json:async()=>({rows:Array.from({length:Math.min(24,30-offset)},(_,j)=>({id:String(offset+j).padStart(26,'0'),title:offset+j===0?'Parent task':'Task '+(offset+j),kind:'TASK',status:null,actor:null,nested:offset+j===0?3:null})),counts:{open:30},offset,total:30,next:offset===0?24:null,watermark:{sequence:1}})};
  }
  if(url.pathname==='/api/screen' && url.searchParams.get('screen')==='home')return {ok:true,json:async()=>({rows:[{id:entries[0].id,title:'Task in flight',kind:'TASK',status:'IN FLIGHT',actor:'worker',group:'in_flight'},{id:entries[1].id,title:'Choose delivery',kind:'DECISION',status:'OPEN',actor:'owner',group:'awaiting_decision'}],counts:{READY:10,BLOCKED:2,'IN FLIGHT':1},offset:0,total:2,previous:null,next:null})};
  if(url.pathname==='/api/screen') {
    const offset=Number(url.searchParams.get('offset')||0);
    return {ok:true,json:async()=>({rows:Array.from({length:Math.min(24,60-offset)},(_,j)=>({id:String(offset+j).padStart(26,'0'),sequence:offset+j+1,title:'Task preview '+(offset+j),kind:'TASK',status:null,actor:null})),counts:{records:60,READY:null},offset,total:60,next:offset+24<60?offset+24:null,note:null,watermark:{sequence:1}})};
  }
  if(url.pathname==='/api/times')return {ok:true,json:async()=>({watermark:{sequence:timeSequence,head:{command_id:String(timeSequence).padStart(26,'0')}},times:{'1:0':{state:'known',value:'2026-01-01T00:00:00Z'}}})};
  throw Error('unexpected request '+raw);
};
const location={hash:'#/gallery'};
const stored=new Map([["whosaidso-theme","dark"]]);
const context=vm.createContext({console,Node,document:doc,window:win,location,history:{replaceState(){}},URL,URLSearchParams,AbortController,fetch,innerWidth:1200,innerHeight:900,sessionStorage:{getItem(){return null;},setItem(){}},localStorage:{getItem:key=>stored.get(key)||null,setItem:(key,value)=>stored.set(key,value)},navigator:{clipboard:{writeText:async text=>{copied=text;}}},matchMedia:()=>({matches:false}),requestAnimationFrame:fn=>fn(),setTimeout:(fn,ms)=>{timers.set(++serial,{fn,ms});return serial;},clearTimeout:id=>timers.delete(id),setInterval:(fn,ms)=>{intervals.set(++serial,{fn,ms});return serial;},clearInterval:id=>intervals.delete(id)});
const observers=new Set();
class IntersectionObserver {
 constructor(fn,options){this.options=options;this.fn=fn;this.nodes=new Set();observers.add(this);}
 observe(node){this.nodes.add(node);observers.add(this); }
 disconnect(){this.nodes.clear();observers.delete(this);}
}
context.IntersectionObserver=IntersectionObserver;
const visible=node=>{for(const observer of [...observers]) if([...observer.nodes].some(n=>node.contains(n))) observer.fn([...observer.nodes].map(target=>({target,isIntersecting:node.contains(target)})));};
const modules=new Map(),root=path.resolve('.');
async function moduleAt(file){if(!modules.has(file))modules.set(file,readFile(file,'utf8').then(source=>new vm.SourceTextModule(source,{context,identifier:file})));return modules.get(file);}
const main=await moduleAt(path.join(root,'ui.js'));
await main.link((spec,parent)=>moduleAt(path.resolve(path.dirname(parent.identifier),spec)));await main.evaluate();
const settle=async()=>{for(let i=0;i<50;i++)await Promise.resolve();};await settle();
const tick=async()=>{for(const {fn} of [...intervals.values()])await fn();await settle();};
const find=(tag,text)=>flatten(body).find(n=>n.tagName===tag.toUpperCase()&&n.textContent===text);
const theme=doc.getElementById('theme-toggle');
assert.equal(doc.documentElement.dataset.theme,'dark','saved theme overrides the system preference');
assert.equal(theme.children.length,1,'theme is one icon');assert.equal(theme.firstChild.tagName,'SVG');assert.equal(theme.textContent,'');
const themeIcon=theme.firstChild.firstChild.getAttribute('d');theme.fire('click');
assert.equal(doc.documentElement.dataset.theme,'light');assert.equal(stored.get('whosaidso-theme'),'light','theme persists');assert.equal(theme.getAttribute('aria-label'),'Switch to dark mode');assert.notEqual(theme.firstChild.firstChild.getAttribute('d'),themeIcon,'icon indicates the other theme');
theme.fire('click');assert.equal(doc.documentElement.dataset.theme,'dark');assert.equal(stored.get('whosaidso-theme'),'dark');
assert.equal(doc.getElementById('project-name').textContent,'test/viewer','switcher must render before the slow gallery response');
assert.ok(doc.getElementById('main').textContent.includes('Loading gallery'),'slow startup shows progress');assert.ok(body.querySelector('.loading'),'loading uses the centered shared content state');assert.equal(body.querySelector('.notice'),null,'loading is not a separate card');assert.equal(theme.parentElement.className,'page-header','loading shares the page title row');
assert.equal(requests[0].searchParams.get('summary'),'0','project discovery must avoid status construction');
release();await settle();
assert.ok(!/null|undefined/.test(doc.getElementById('main').textContent),'gallery omits absent values');assert.ok(body.textContent.includes('File not on this machine'),'missing files use a quiet state');assert.ok(!body.textContent.includes('no such file or directory'),'missing files never show raw filesystem errors');
const cards=body.querySelector('.gallery-cards');assert.equal(cards?.children.length,24,'only one gallery page is mounted');
assert.equal(cards.firstChild.querySelector('img').src,undefined,'offscreen images do not compete for decode slots');
visible(cards.firstChild);visible(cards.children[5]);await settle();
assert.equal(cards.children[1].querySelector('img').src,undefined,'only visible thumbnails start');
assert.equal(cards.firstChild.querySelector('img').fetchPriority,'high','visible images have priority');
const inlineVideo=cards.children[6].querySelector('video');assert.equal(inlineVideo.controls,true,'videos have native controls in the card');visible(cards.children[6]);await settle();assert.ok(inlineVideo.src&&inlineVideo.loaded,'visible video requests its poster frame');cards.children[6].querySelector('.artifact-preview').fire('click',{target:inlineVideo});assert.equal(body.querySelector('.artifact-dialog'),null,'native video interaction stays inside card');
const textPreview=cards.children[5].querySelector('pre');assert.ok(textPreview.textContent.includes('Could not read file'));assert.ok(textPreview.querySelector('button'),'failed text previews have a retry action');textFail=false;textPreview.querySelector('button').fire('click');await settle();assert.equal(textPreview.textContent,'synthetic text');
assert.ok(cards.children[7].querySelector('a').href.includes('/download?'),'all pinned types can download');
const byLabel=(node,label)=>flatten(node).find(n=>n.getAttribute('aria-label')===label);
const actions=cards.firstChild.querySelector('.gallery-actions');
assert.deepEqual(actions.children.map(n=>n.getAttribute('aria-label')),['Reveal in file manager','Copy path','Download','Delete']);
assert.ok(actions.children.every(n=>n.textContent===''),'file actions are icon only');
assert.equal(cards.firstChild.querySelector('.artifact-title-button').querySelector('svg'),null,'title is text only');
const first=cards.firstChild,img=first.querySelector('img');
assert.ok(img.src.startsWith('/api/gallery/thumb?'),'cards must request thumbnails');
assert.ok(!requests.some(u=>u.pathname==='/api/view'),'startup must not fetch full todo/show');
img.fire('error');assert.equal(img.hidden,true);const retry=find('button','Retry preview');assert.ok(retry&&!retry.hidden,'failed previews have a retry action');
retry.fire('click');assert.equal(img.hidden,true,'image stays blank until loaded');assert.equal(retry.hidden,true);img.fire('load');assert.equal(img.hidden,false);assert.equal(first.querySelector('.gallery-load-status').textContent,'');
const title=first.querySelector('.artifact-title-button');title.fire('click');await settle();
assert.equal(body.querySelector('.artifact-dialog'),null,'title reveals instead of opening details');
assert.ok(requests.some(u=>u.pathname==='/api/gallery/reveal'),'title reveals the file');
first.querySelector('.artifact-preview').fire('click');await settle();
let dialog=body.querySelector('.artifact-dialog');assert.ok(dialog?.open);
assert.ok(dialog.querySelector('img').src.startsWith('/api/gallery/file?'),'inspector requests the full original');
assert.equal(dialog.querySelector('aside'),null,'item view has no metadata sidebar');
assert.equal(dialog.querySelector('details'),null,'item view has no hidden metadata fields');
assert.equal(dialog.querySelector('nav'),null,'item view has no page controls or counts');
assert.equal(dialog.querySelector('.artifact-title-button').querySelector('svg'),null,'item title is text only');
assert.deepEqual(dialog.querySelector('.gallery-actions').children.map(n=>n.getAttribute('aria-label')),['Reveal in file manager','Copy path','Download','Delete']);
assert.ok(byLabel(dialog,'Download').href.startsWith('/api/gallery/download?'));
byLabel(dialog,'Copy path').fire('click');await settle();assert.equal(copied,'shot-0.png','copy path is platform neutral');
const reveals=requests.filter(u=>u.pathname==='/api/gallery/reveal').length;
dialog.querySelector('.artifact-title-button').fire('click');await settle();assert.equal(requests.filter(u=>u.pathname==='/api/gallery/reveal').length,reveals+1,'item title reveals the file');
dialog.close();assert.equal(first.querySelector('.artifact-preview').focusArgs?.preventScroll,true,'closing preserves scroll');
cards.children[3].querySelector('.artifact-preview').fire('click');dialog=body.querySelector('.artifact-dialog');assert.ok(dialog.querySelector('img')?.src.startsWith('/api/gallery/file?'),'opening UNKNOWN requests fresh verification');dialog.close();
cards.children[4].querySelector('.artifact-preview').fire('click');dialog=body.querySelector('.artifact-dialog');assert.equal(byLabel(dialog,'Copy path').disabled,true,'unknown path cannot be copied');dialog.close();
cards.children[6].querySelector('.artifact-preview').fire('click');const videoDialog=body.querySelector('.artifact-dialog'),video=videoDialog.querySelector('video');assert.ok(video?.loaded,'video loads only when opened');videoDialog.close();assert.equal(video.paused,true,'closing inspection stops playback');
const galleryReads=requests.filter(u=>u.pathname==='/api/gallery').length;await tick();assert.equal(requests.filter(u=>u.pathname==='/api/gallery').length,galleryReads,'gallery does not poll');
const scroller=doc.getElementById('main');scroller.scrollTop=321;const focused=theme;focused.focus();
visible(body.querySelector('.gallery-sentinel'));await settle();assert.equal(cards.children.length,48,'near boundary loads next batch');assert.equal(cards.firstChild,first,'append preserves existing media nodes');assert.equal(scroller.scrollTop,321,'append preserves scrollbar drag');assert.equal(doc.activeElement,focused,'append preserves focus');
const sentinelObserver=[...observers].find(o=>o.nodes.has(body.querySelector('.gallery-sentinel')));assert.ok(sentinelObserver.options.rootMargin==='1800px','stream loads two screens ahead');
const remote=entries[40];entries.splice(40,1);byLabel(body,'Refresh').fire('click');await settle();assert.equal(cards.children.length,24,'explicit refresh resets changed membership');
visible(body.querySelector('.gallery-sentinel'));await settle();assert.equal(cards.children.length,48);assert.ok(![...cards.children].some(n=>n.querySelector('.artifact-title-button').textContent===remote.title),'refresh removes remote retractions');
const prefixFirst=cards.firstChild;scroller.scrollTop=321;theme.focus();let finishBatch;delayGallery=new Promise(resolve=>{finishBatch=resolve;});visible(body.querySelector('.gallery-sentinel'));await settle();entries.splice(55,1);finishBatch();await settle();assert.equal(cards.children.length,48,'a changed revision cannot mix prefixes or replace the existing stream');assert.equal(cards.firstChild,prefixFirst,'concurrent writes preserve existing nodes');assert.equal(scroller.scrollTop,321,'concurrent writes preserve scrollbar drag');assert.equal(doc.activeElement,theme,'concurrent writes preserve focus');assert.ok(body.textContent.includes('New changes available'),'changed data offers explicit refresh');
byLabel(body,'Refresh').fire('click');await settle();assert.equal(cards.children.length,24);visible(body.querySelector('.gallery-sentinel'));await settle();assert.equal(cards.children.length,48);
refuseDelete=true;const refused=cards.firstChild;byLabel(refused,'Delete').fire('click');await settle();assert.ok(refused.textContent.includes('Review required')&&refused.className.split(' ').includes('has-error'),'refused deletion appears on the clicked card');assert.equal(refused.querySelector('.action-error').getAttribute('role'),'alert');refuseDelete=false;
byLabel(cards.firstChild,'Delete').fire('click');await settle();assert.equal(cards.children.length,47,'admitted delete removes the card immediately');assert.equal(cards.firstChild.querySelector('.artifact-title-button').textContent,'Artifact 1');
cards.firstChild.querySelector('.artifact-preview').fire('click');
byLabel(body.querySelector('.artifact-dialog'),'Delete').fire('click');await settle();assert.equal(body.querySelector('.artifact-dialog'),null,'item deletion closes its view');assert.equal(cards.children.length,46,'item deletion removes the matching card');
cards.firstChild.querySelector('.artifact-preview').fire('click');
for(const screen of ['home','tasks','records','history']){location.hash='#/'+screen;win.fire('hashchange',{oldURL:'http://viewer/#/gallery'});await settle();assert.ok(body.querySelector(screen==='home'?'.overview-screen':'.browser-screen'),screen+' mounts bounded screen');assert.equal(theme.parentElement.className,'page-header','theme shares page title row');assert.ok(!/null|undefined/.test(doc.getElementById('main').textContent),screen+' omits absent values');assert.equal(find('button','Next'),undefined,'no screen uses a pager');
 if(screen==='tasks'||screen==='records'){const request=requests.filter(u=>u.pathname==='/api/screen').at(-1);assert.equal(request.searchParams.get('screen'),screen==='tasks'?'tasks':'records','task and record routes select distinct bounded lists'); if(screen==='tasks'){const parent=find('a','Parent task');assert.equal(parent.href,'#/tasks?under='+String(0).padStart(26,'0'),'clicking a parent drills into prerequisites');assert.ok(body.textContent.includes('3 open nested'),'parent shows nested count');assert.ok(!body.textContent.includes('0 open nested'),'an absent count is not rounded to zero');}}
 if(screen!=='home'){
  const list=body.querySelector('.browser-rows'),firstRow=list.firstChild;scroller.scrollTop=222;theme.focus();
  visible(body.querySelector('.gallery-sentinel'));await settle();assert.equal(list.firstChild,firstRow,screen+' append preserves first node');assert.equal(scroller.scrollTop,222,screen+' append preserves scroll');assert.equal(doc.activeElement,theme,screen+' append preserves focus');assert.ok(list.children.length>24,screen+' scroll loads another batch');
 }
 if(screen==='home'){
  assert.equal(body.querySelector('.artifact-dialog'),null,'routing closes the inspector');
  assert.equal(body.querySelectorAll('h2').length,2,'overview has only flight and decision panels');
  assert.ok(body.textContent.includes('worker')&&body.textContent.includes('Choose delivery'),'owner sees holders and decisions');
  assert.equal(body.querySelector('.search-input'),null,'overview has no record browser');
  assert.ok(!body.textContent.includes('Load work status totals'),'counts are ready at first paint');
  await tick();
  assert.ok(!requests.some(u=>u.searchParams.get('status_counts')==='1'),'overview needs no second totals read');
 }
}
assert.ok(!requests.some(u=>u.pathname==='/api/view'),'other screens must not fetch bulk models');
assert.ok(requests.filter(u=>u.pathname==='/api/screen').length>=3);
location.hash='#/record/'+entries[0].id;win.fire('hashchange',{oldURL:'http://viewer/#/history'});await settle();
assert.ok(body.textContent.includes('synthetic absent record'),'record details use scoped reads');assert.ok(!/null|undefined/.test(scroller.textContent),'detail omits absent values');
let at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}).toISOString(),'2026-01-01T00:00:00.000Z');
timeSequence=999;at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}),null,'timestamps from another prefix remain UNKNOWN');
timeSequence=0;recordSequence=0;main.namespace.state.cache.clear();at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}),null,'two unavailable prefixes never establish a time');recordSequence=1;
const reads=requests.filter(u=>u.pathname==='/api/view').length;recordSequence++;await tick();assert.ok(requests.filter(u=>u.pathname==='/api/view').length>reads,'changed ledger refreshes detail');
assert.ok(requests.filter(u=>u.pathname==='/api/view').every(u=>u.searchParams.get('id')),'detail must never load unscoped views');
assert.ok(!body.textContent.includes('Reveal in Finder'),'file action labels work on every platform');
for(const n of flatten(body).filter(n=>n.tagName==='PATH'))assert.notEqual(n.attrs.d,'undefined','every icon exists');
location.hash='#/gallery';win.fire('hashchange',{oldURL:'http://viewer/#/record/example'});await settle();
find('button','All types').fire('click');find('button','Videos').fire('click');
for(const [id,timer] of [...timers])if(timer.ms===250){timers.delete(id);timer.fn();}await settle();
assert.ok(flatten(body).find(n=>n.matches('.select')&&n.textContent==='Videos'),'selected gallery filter stays visible');assert.equal(requests.filter(u=>u.pathname==='/api/gallery').at(-1).searchParams.get('kind'),'video');assert.equal(body.querySelector('.gallery-cards').children.length,1,'the selected gallery kind is requested');
location.hash='#/records';win.fire('hashchange',{oldURL:'http://viewer/#/gallery'});await settle();find('button','All kinds').fire('click');find('button','Claims').fire('click');
for(const [id,timer] of [...timers])if(timer.ms===250){timers.delete(id);timer.fn();}await settle();assert.ok(flatten(body).find(n=>n.matches('.select')&&n.textContent==='Claims'),'selected record filter stays visible');assert.equal(requests.filter(u=>u.pathname==='/api/screen').at(-1).searchParams.get('kind'),'CLAIM');
location.hash='#/tasks?under='+String(999).padStart(26,'0');win.fire('hashchange',{oldURL:'http://viewer/#/records'});await settle();assert.ok(body.textContent.includes('Task unavailable at this watermark'),'unknown task navigation retains its reason');assert.ok(!body.textContent.includes('No entries match'),'unknown navigation is not an empty filter result');
console.log('PASS: bounded startup, prompt switcher, thumbnails/originals, media recovery, explicit refresh and stable appends, keyboard/swipe inspection, save/copy actions, all bounded project screens');
