import {version,factor} from '/assets/version.js';
const worker=new Worker('/assets/worker.js');
const wasm=await WebAssembly.instantiateStreaming(fetch('/assets/add.wasm'));
const data=await fetch('/data.json').then(r=>r.json());
const module=await import('./version.js');
window.fixtureScenario={version,data,module:module.version,wasm:wasm.instance.exports.add(4,6),worker:null,network:'pending'};
const calculate=()=>{const n=Number(document.querySelector('#budget').value);worker.postMessage(n);};
worker.onmessage=e=>{window.fixtureScenario.worker=e.data;document.querySelector('#result').textContent=`Version ${version}: ${e.data.value} retry units; WASM sum ${window.fixtureScenario.wasm}`;window.dispatchEvent(new Event('scenario.changed'));};
document.querySelector('#simulate').addEventListener('click',calculate);calculate();
try{const {h,render}=await import('https://esm.sh/preact@10.27.2');render(h('span',{},`External Preact framework rendered version ${version}`),document.querySelector('#framework'));window.fixtureScenario.network='pass';}catch(e){document.querySelector('#framework').textContent='External framework unavailable; local simulation remains usable';window.fixtureScenario.network='unavailable';}
