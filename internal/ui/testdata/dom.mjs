// Minimal headless DOM for viewer behavior. Layout, paint and native downloads
// are outside this instrument; requests, handlers, focus and state are observed.
export class Node {
  constructor(tag, text = '') { this.tagName=tag.toUpperCase();this.children=[];this.attrs={};this.dataset={};this.handlers={};this.style={setProperty(k,v){this[k]=v;}};this.className='';this.value=text;this.hidden=false; }
  append(...items) { for(const item of items.flat(Infinity)) {const child=item instanceof Node?item:new Node('#text',String(item));child.remove();child.parentElement=this;this.children.push(child);} }
  replaceChildren(...items){for(const child of this.children)child.parentElement=null;this.children=[];this.append(...items);}
  remove(){if(this.parentElement){this.parentElement.children.splice(this.parentElement.children.indexOf(this),1);this.parentElement=null;}}
  insertBefore(child,at){child.remove();child.parentElement=this;this.children.splice(at?this.children.indexOf(at):this.children.length,0,child);}
  get firstChild(){return this.children[0]||null;}
  get lastChild(){return this.children.at(-1)||null;}
  get nextSibling(){return this.parentElement?.children[this.parentElement.children.indexOf(this)+1]||null;}
  get isConnected(){return this.tagName==='BODY'||!!this.parentElement?.isConnected;}
  get textContent(){return this.tagName==='#TEXT'?this.value:this.children.map(c=>c.textContent).join('');}
  set textContent(value){this.replaceChildren(String(value));}
  get classList(){const self=this;const change=(key,on)=>{const all=new Set(self.className.split(' '));on?all.add(key):all.delete(key);self.className=[...all].join(' ');};return {add:k=>change(k,true),remove:k=>change(k,false),toggle:(k,on)=>change(k,on)};}
  setAttribute(k,v){this.attrs[k]=String(v);if(k==='class')this.className=String(v);}
  getAttribute(k){return this.attrs[k];}
  removeAttribute(k){delete this.attrs[k];}
  matches(selector){return selector.split(',').some(raw=>{const s=raw.trim();if(s.startsWith('.'))return this.className.split(' ').includes(s.slice(1));if(s==='[data-nav]')return 'nav' in this.dataset;return this.tagName===s.toUpperCase();});}
  querySelectorAll(s){return this.children.flatMap(c=>[...(c.matches(s)?[c]:[]),...c.querySelectorAll(s)]);}
  querySelector(s){return this.querySelectorAll(s)[0]||null;}
  closest(s){return this.matches(s)?this:this.parentElement?.closest(s);}
  contains(n){return n===this||this.children.some(c=>c.contains(n));}
  addEventListener(type,fn,opts){(this.handlers[type]??=[]).push({fn,once:opts?.once});}
  removeEventListener(type,fn){this.handlers[type]=(this.handlers[type]||[]).filter(h=>h.fn!==fn);}
  fire(type,extra={}){if(this.disabled)return;const e={target:this,stopPropagation(){},preventDefault(){this.defaultPrevented=true;},...extra};for(const h of [...this.handlers[type]||[]]){h.fn(e);if(h.once)this.removeEventListener(type,h.fn);}return e;}
  focus(options){this.focusArgs=options;this.onFocus?.(this);}
  showModal(){this.open=true;}
  close(){this.open=false;this.fire('close');}
  pause(){this.paused=true;}
  load(){this.loaded=true;}
  getBoundingClientRect(){return {top:20,left:20,right:200,bottom:60,height:40};}
}
export const flatten = n => [n,...n.children.flatMap(flatten)];
