'use strict';
// Execute the complete trusted-host script with deterministic transport and DOM boundaries.
const {test}=require('node:test'),assert=require('node:assert/strict');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const {webcrypto}=require('node:crypto');
const sourcePath=process.env.HAERYONG_HOST_SOURCE||path.join(__dirname,'host.js');
const source=fs.readFileSync(sourcePath,'utf8');
const key='haeryong.viewer.v1',saved='synthetic-remembered-owner';
async function host(initial=200,native=false){
 const values=new Map([[key,saved]]),sessionValues=new Map(),nodes=new Map(),calls=[];
 const storage=map=>({getItem:k=>map.get(k)??null,setItem:(k,v)=>map.set(k,String(v)),removeItem:k=>map.delete(k)});
 function node(name){if(!nodes.has(name))nodes.set(name,{disabled:false,hidden:false,textContent:'',dataset:{},style:{},setAttribute(){},append(){},remove(){this.removed=true;},addEventListener(){}});return nodes.get(name);}
 const state={session:initial,api:200};
 async function response(input){const url=String(input);calls.push(url);if(url==='/pair')throw new Error('Unexpected pairing');let code=url==='/session'?state.session:state.api;if(code==='transport')throw new Error('Synthetic transport unavailable');if(url==='/viewer/config'&&code===200)return {status:200,ok:true,json:async()=>({instance:'same-instance',implementation:'same-code',renderers:['http://127.0.0.1:9272','http://127.0.0.1:9273']})};return {status:code,ok:code>=200&&code<300,json:async()=>({id:'same-owner'})};}
 const sandbox={URL,URLSearchParams,DOMException,TextEncoder,TextDecoder,crypto:webcrypto,console,
  localStorage:storage(values),sessionStorage:storage(sessionValues),location:{href:'http://127.0.0.1:9270/'+(native?'?native=1':''),origin:'http://127.0.0.1:9270',pathname:'/',search:''},history:{replaceState(){}},navigator:{userAgent:'Deterministic host test',platform:'test'},
  document:{querySelector:node,querySelectorAll:()=>[],createElement:node,body:node('body'),visibilityState:'visible',addEventListener(){}},
  addEventListener(){},setInterval:()=>1,clearInterval(){},setTimeout,clearTimeout,fetch:response};
 sandbox.window=sandbox;if(native)sandbox.fixtureNativeRequest=response;
 const context=vm.createContext(sandbox);vm.runInContext(source,context,{filename:sourcePath});
 const drain=async()=>{for(let i=0;i<12;i++)await new Promise(resolve=>setImmediate(resolve));};await drain();
 return {context,values,nodes,calls,state,drain,read:expression=>vm.runInContext(expression,context)};
}
for(const native of [false,true])for(const failure of [503,'transport'])test(`initial ${native?'native proxy':'browser'} ${failure} retains remembered access`,async()=>{
 const h=await host(failure,native);assert.equal(h.values.get(key),saved);assert.equal(h.read('token'),saved);assert.equal(h.calls.filter(p=>p==='/pair').length,0);assert.equal(h.read('sessionEnded'),false);
 h.state.session=200;await h.read('connect()');assert.equal(h.values.get(key),saved);assert.equal(h.calls.filter(p=>p==='/pair').length,0);
});
test('initial definitive401 permits reconnect',async()=>{const h=await host(401);assert.equal(h.values.has(key),false);assert.equal(h.read('token'),'');assert.equal(h.calls.filter(p=>p==='/pair').length,1);});
for(const failure of [503,'transport',403])test(`connected ${failure} preserves token, views and enabled controls`,async()=>{
 const h=await host();h.read("views.set('a',{version:'a',state:{page_path:'pages/scenario.html',page_query:'',page_fragment:''},binding:{page_path:'pages/scenario.html'},errors:[],commands:new Map(),sealedRefs:new Map(),iframe:{remove(){}},section:{remove(){}},closed:false})");
 const view=h.read("views.get('a')"),generation=h.read("rendererOperations.get('a').generation");h.state.api=failure;await assert.rejects(h.read("api('/viewer/config')"));assert.equal(h.read("views.get('a')"),view);assert.equal(h.read('sessionEnded'),false);assert.equal(h.read("rendererOperations.get('a').generation"),generation);assert.equal(h.values.get(key),saved);assert.equal(h.nodes.get('#run').disabled,false);
 h.state.api=200;await h.read("api('/viewer/config')");assert.equal(h.read("views.get('a')"),view);
});
test('connected definitive401 ends access and disposes views',async()=>{
 const h=await host();h.read("views.set('a',{version:'a',state:{page_path:'pages/scenario.html',page_query:'',page_fragment:''},binding:{page_path:'pages/scenario.html'},errors:[],commands:new Map(),sealedRefs:new Map(),iframe:{remove(){}},section:{remove(){}},closed:false})");h.state.api=401;await assert.rejects(h.read("api('/viewer/config')"));assert.equal(h.read('views.size'),0);assert.equal(h.read('sessionEnded'),true);assert.equal(h.values.has(key),false);assert.equal(h.nodes.get('#run').disabled,true);assert.equal(h.nodes.get('#reopen').disabled,true);
});
