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
doc.getElementById=id=>{if(!ids.has(id)){const n=create(id==='project-menu'?'div':'button');n.id=id;n.hidden=id==='project-menu';body.append(n);ids.set(id,n);}return ids.get(id);};
doc.querySelector=s=>body.querySelector(s);doc.querySelectorAll=s=>body.querySelectorAll(s);
const win=new Node('window');Object.assign(win,{scrollY:0,scrollTo(){}});
const timers=new Map(),intervals=new Map();let serial=0;
const requests=[];let hold,release,delayGallery=null,fail=false,copied='',recordSequence=1,timeSequence=1,textFail=true;
const entry=i=>({id:String(i).padStart(26,'0'),title:'Artifact '+i,kind:'image',status:'present',path:'shot-'+i+'.png',sha256:'a'.repeat(64),length:20000,added_at:'2026-01-01T00:00:00Z',tasks:[],cap:'fixture',verdict:'unreviewed'});
const entries=Array.from({length:60},(_,i)=>entry(i));entries[3].status='unknown';entries[3].status_reason='not compared in list';entries[4].status='missing';entries[4].path='';entries[5].kind='text';entries[5].path='shot-5.txt';entries[6].kind='video';entries[6].path='shot-6.mp4';entries[7].kind='';entries[7].path='shot-7.pdf';
const gallery=offset=>({entries:entries.slice(offset,offset+24),offset,total:60,previous:offset?Math.max(0,offset-24):null,next:offset+24<60?offset+24:null,task_titles:{}});
hold=new Promise(resolve=>{release=resolve;});
const fetch=async raw=>{
  const url=new URL(raw,'http://viewer');requests.push(url);
  if(url.pathname==='/api/gallery/file' && url.searchParams.get('id')===entries[5].id)return textFail?{ok:false,status:503,statusText:'Unavailable'}:{ok:true,headers:{get(){return null;}},text:async()=> 'synthetic text'};
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
const textPreview=cards.children[5].querySelector('pre');assert.ok(textPreview.textContent.includes('Could not read file'));assert.ok(textPreview.querySelector('button'),'failed text previews have a retry action');textFail=false;textPreview.querySelector('button').fire('click');await settle();assert.equal(textPreview.textContent,'synthetic text');
assert.equal(cards.children[7].querySelector('.artifact-save').disabled,true,'unsupported types must not offer a failing save action');
const first=cards.firstChild,img=first.querySelector('img');
assert.ok(img.src.startsWith('/api/gallery/thumb?'),'cards must request thumbnails');
assert.ok(!requests.some(u=>u.pathname==='/api/view'),'startup must not fetch full todo/show');
img.fire('error');assert.equal(img.hidden,true);const retry=find('button','Retry preview');assert.ok(retry&&!retry.hidden,'failed previews have a retry action');
retry.fire('click');assert.equal(img.hidden,false);assert.equal(retry.hidden,true);img.fire('load');assert.equal(first.querySelector('.gallery-load-status').textContent,'');
const title=first.querySelector('.artifact-title-button');title.fire('click');await settle();
let dialog=body.querySelector('.artifact-dialog');assert.ok(dialog?.open);
assert.ok(dialog.querySelector('img').src.startsWith('/api/gallery/file?'),'inspector requests the full original');
assert.equal(find('button','Previous artifact').disabled,true);
dialog.fire('keydown',{key:'ArrowRight'});assert.equal(dialog.getAttribute('aria-label'),'Artifact 1','right arrow inspects next artifact');
dialog.fire('keydown',{key:'ArrowLeft'});assert.equal(dialog.getAttribute('aria-label'),'Artifact 0');
const stage=dialog.querySelector('.artifact-stage'),dialogBody=dialog.querySelector('.artifact-dialog-body');
dialogBody.fire('pointerdown',{target:stage,pointerType:'touch',clientX:200,clientY:80,pointerId:1});dialogBody.fire('pointerup',{target:stage,clientX:80,clientY:85,pointerId:1});assert.equal(dialog.getAttribute('aria-label'),'Artifact 1','horizontal touch swipe advances');
find('button','Next artifact').fire('click');find('button','Next artifact').fire('click');
assert.equal(dialog.getAttribute('aria-label'),'Artifact 3');assert.ok(dialog.querySelector('img')?.src.startsWith('/api/gallery/file?'),'opening UNKNOWN requests a fresh source verification');
find('button','Next artifact').fire('click');assert.equal(find('button','Copy path').disabled,true,'an unavailable local path cannot be copied as a blank success');dialog.fire('keydown',{key:'ArrowLeft'});
dialog.fire('keydown',{key:'ArrowLeft'});dialog.fire('keydown',{key:'ArrowLeft'});
const save=dialog.querySelector('.artifact-save');assert.ok(save.href.startsWith('/api/gallery/download?'));assert.equal(save.textContent,'Save original');
find('button','Copy path').fire('click');await settle();assert.equal(copied,'shot-1.png','copy path is platform neutral');
dialog.close();assert.equal(title.focusArgs?.preventScroll,true,'closing preserves scroll');
cards.children[6].querySelector('.artifact-title-button').fire('click');const videoDialog=body.querySelector('.artifact-dialog'),video=videoDialog.querySelector('video');assert.ok(video?.loaded,'video loads only when opened');videoDialog.close();assert.equal(video.paused,true,'closing inspection stops playback');
await tick();assert.equal(cards.firstChild,first,'unchanged polls preserve media nodes');
fail=true;await tick();assert.ok(body.textContent.includes('fixture refresh failure'),'refresh failure must be visible');fail=false;
let late;delayGallery=new Promise(resolve=>{late=resolve;});await tick();
find('button','Next').fire('click');await settle();assert.equal(cards.children.length,24);assert.equal(cards.firstChild.querySelector('.artifact-title-button').textContent,'Artifact 24');
late();await settle();assert.equal(cards.firstChild.querySelector('.artifact-title-button').textContent,'Artifact 24','a late poll cannot overwrite navigation');
cards.firstChild.querySelector('.artifact-title-button').fire('click');
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
