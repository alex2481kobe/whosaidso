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
const requests=[];let hold,release,delayGallery=null,fail=false,copied='',recordSequence=1,timeSequence=1,textFail=true;
const entry=i=>({id:String(i).padStart(26,'0'),title:'Artifact '+i,kind:'image',status:'present',path:'shot-'+i+'.png',sha256:'a'.repeat(64),length:20000,added_at:'2026-01-01T00:00:00Z',tasks:[],cap:'fixture',verdict:'unreviewed'});
const entries=Array.from({length:60},(_,i)=>entry(i));entries[3].status='unknown';entries[3].status_reason='not compared in list';entries[4].status='missing';entries[4].path='';entries[5].kind='text';entries[5].path='shot-5.txt';entries[6].kind='video';entries[6].path='shot-6.mp4';entries[7].kind='';entries[7].path='shot-7.pdf';
const gallery=offset=>({entries:entries.slice(offset,offset+24),offset,total:60,previous:offset?Math.max(0,offset-24):null,next:offset+24<60?offset+24:null,task_titles:{}});
hold=new Promise(resolve=>{release=resolve;});
const fetch=async (raw,options={})=>{
  const url=new URL(raw,'http://viewer');requests.push(url);
  if(url.pathname==='/api/gallery/file' && url.searchParams.get('id')===entries[5].id)return textFail?{ok:false,status:503,statusText:'Unavailable'}:{ok:true,headers:{get(){return null;}},text:async()=> 'synthetic text'};
  if(url.pathname==='/api/gallery/reveal')return {ok:true,json:async()=>({revealed:url.searchParams.get('id')})};
  if(url.pathname==='/api/gallery/remove'){const id=url.searchParams.get('id');entries.splice(entries.findIndex(e=>e.id===id),1);return {ok:true,json:async()=>({removed:id})};}
  if(url.pathname==='/api/projects')return {ok:true,json:async()=>({current:'test/viewer',projects:[{id:'test/viewer',available:true}]})};
  if(url.pathname==='/api/gallery'){
    if(hold){await hold;hold=null;}
    if(delayGallery){const pending=delayGallery;delayGallery=null;await pending;}
    if(fail)return {ok:false,status:500,json:async()=>({error:{message:'fixture refresh failure'}})};
    return {ok:true,json:async()=>gallery(Number(url.searchParams.get('offset')))};
  }
  if(url.pathname==='/api/screen' && url.searchParams.get('screen')==='status')return {ok:true,json:async()=>({watermark:{sequence:recordSequence,head:{command_id:String(recordSequence).padStart(26,'0')}}})};
  if(url.pathname==='/api/view' && url.searchParams.get('id'))return {ok:true,json:async()=>url.searchParams.get('view')==='history'?{events:[],watermark:{sequence:recordSequence,head:{command_id:String(recordSequence).padStart(26,'0')}}}:{result:'UNKNOWN',reason:'synthetic absent record',records:[],sources:[]}};
  if(url.pathname==='/api/screen') {
    const allowed=new Set(['project','screen','offset','search','kind','status','sort','status_counts']);
    for(const key of url.searchParams.keys()) if(!allowed.has(key)) return {ok:false,status:400,json:async()=>({error:{message:'invalid screen parameter '+key}})};
  }
  if(url.pathname==='/api/screen')return {ok:true,json:async()=>({rows:[{id:entries[0].id,title:'Task preview',kind:'TASK',status:'READY',actor:'contributor'}],counts:{records:60,READY:60},offset:0,total:60,previous:null,next:24,note:'One page'})};
  if(url.pathname==='/api/times')return {ok:true,json:async()=>({watermark:{sequence:timeSequence,head:{command_id:String(timeSequence).padStart(26,'0')}},times:{'1:0':{state:'known',value:'2026-01-01T00:00:00Z'}}})};
  throw Error('unexpected request '+raw);
};
const location={hash:'#/gallery'};
const context=vm.createContext({console,Node,document:doc,window:win,location,history:{replaceState(){}},URL,URLSearchParams,AbortController,fetch,innerWidth:1200,innerHeight:900,sessionStorage:{getItem(){return null;},setItem(){}},localStorage:{getItem(){return null;},setItem(){}},navigator:{clipboard:{writeText:async text=>{copied=text;}}},matchMedia:()=>({matches:false}),requestAnimationFrame:fn=>fn(),setTimeout:(fn,ms)=>{timers.set(++serial,{fn,ms});return serial;},clearTimeout:id=>timers.delete(id),setInterval:(fn,ms)=>{intervals.set(++serial,{fn,ms});return serial;},clearInterval:id=>intervals.delete(id)});
const observers=new Set();
class IntersectionObserver {
 constructor(fn){this.fn=fn;this.nodes=new Set();observers.add(this);}
 observe(node){this.nodes.add(node);}
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
assert.equal(doc.getElementById('project-name').textContent,'test/viewer','switcher must render before the slow gallery response');
assert.ok(doc.getElementById('main').textContent.includes('Loading gallery'),'slow startup shows progress');
assert.equal(requests[0].searchParams.get('summary'),'0','project discovery must avoid status construction');
release();await settle();
const cards=body.querySelector('.gallery-cards');assert.equal(cards?.children.length,24,'only one gallery page is mounted');
assert.equal(cards.firstChild.querySelector('img').src,undefined,'offscreen images do not compete for decode slots');
visible(cards.firstChild);visible(cards.children[5]);await settle();
assert.equal(cards.children[1].querySelector('img').src,undefined,'only visible thumbnails start');
assert.equal(cards.firstChild.querySelector('img').fetchPriority,'high','visible images have priority');
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
retry.fire('click');assert.equal(img.hidden,false);assert.equal(retry.hidden,true);img.fire('load');assert.equal(first.querySelector('.gallery-load-status').textContent,'');
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
await tick();assert.equal(cards.firstChild,first,'unchanged polls preserve media nodes');
fail=true;await tick();assert.ok(body.textContent.includes('fixture refresh failure'),'refresh failure must be visible');fail=false;
let late;delayGallery=new Promise(resolve=>{late=resolve;});await tick();
visible(body.querySelector('.gallery-sentinel'));await settle();assert.equal(cards.children.length,48,'near-bottom loads the next batch');assert.equal(cards.children[24].querySelector('.artifact-title-button').textContent,'Artifact 24');
late();await settle();assert.equal(cards.children.length,48,'a late poll cannot overwrite the appended batch');
assert.equal(find('button','Next'),undefined,'gallery has no page controls');
assert.ok(!body.textContent.includes('of 60'),'gallery has no page counts');
byLabel(cards.firstChild,'Delete').fire('click');await settle();assert.equal(cards.children.length,47,'admitted delete removes the card immediately');assert.equal(cards.firstChild.querySelector('.artifact-title-button').textContent,'Artifact 1');
cards.firstChild.querySelector('.artifact-preview').fire('click');
byLabel(body.querySelector('.artifact-dialog'),'Delete').fire('click');await settle();assert.equal(body.querySelector('.artifact-dialog'),null,'item deletion closes its view');assert.equal(cards.children.length,46,'item deletion removes the matching card');
cards.firstChild.querySelector('.artifact-preview').fire('click');
for(const screen of ['home','records','history']){location.hash='#/'+screen;win.fire('hashchange',{oldURL:'http://viewer/#/gallery'});await settle();assert.ok(body.querySelector('.browser-screen'),screen+' mounts bounded screen');
 if(screen==='home'){assert.equal(body.querySelector('.artifact-dialog'),null,'routing closes the inspector');find('button','Load work status totals').fire('click');await settle();const full=requests.filter(u=>u.searchParams.get('status_counts')==='1').length;assert.equal(full,1,'status totals load on explicit request');await tick();assert.equal(requests.filter(u=>u.searchParams.get('status_counts')==='1').length,full,'polls must not recompute unrequested totals');}
}
assert.ok(!requests.some(u=>u.pathname==='/api/view'),'other screens must not fetch bulk models');
assert.ok(requests.filter(u=>u.pathname==='/api/screen').length>=3);
location.hash='#/record/'+entries[0].id;win.fire('hashchange',{oldURL:'http://viewer/#/history'});await settle();
assert.ok(body.textContent.includes('synthetic absent record'),'record details use scoped reads');
let at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}).toISOString(),'2026-01-01T00:00:00.000Z');
timeSequence=999;at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}),null,'timestamps from another prefix remain UNKNOWN');
timeSequence=0;recordSequence=0;main.namespace.state.cache.clear();at=await main.namespace.times(entries[0].id);assert.equal(at({sequence:1,event_index:0}),null,'two unavailable prefixes never establish a time');recordSequence=1;
const reads=requests.filter(u=>u.pathname==='/api/view').length;recordSequence++;await tick();assert.ok(requests.filter(u=>u.pathname==='/api/view').length>reads,'changed ledger refreshes detail');
assert.ok(requests.filter(u=>u.pathname==='/api/view').every(u=>u.searchParams.get('id')),'detail must never load unscoped views');
assert.ok(!body.textContent.includes('Reveal in Finder'),'file action labels work on every platform');
for(const n of flatten(body).filter(n=>n.tagName==='PATH'))assert.notEqual(n.attrs.d,'undefined','every icon exists');
console.log('PASS: bounded startup, prompt switcher, thumbnails/originals, media recovery, stable polling, keyboard/swipe inspection, save/copy actions, all bounded project screens');
