import {test} from "node:test";
import assert from "node:assert/strict";
import {QuestionSubmission,questionUnresolved,BrowserQuestionJournal} from "./questions.ts";
import {JournalReadError} from "./transfer.ts";
import type {QuestionRecord} from "./questions.ts";
import {ApiError,NetworkError} from "./types.ts";
import type {HaeryongClient,QuestionView,ListPage} from "./types.ts";
import {QuestionHistory} from "../app/screens/question-history.ts";

const journal=()=>{const rows=new Map<string,QuestionRecord>();return {read:()=>[...rows.values()].map(row=>structuredClone(row)),save:(row:QuestionRecord)=>{rows.set(row.operationId,structuredClone(row));}};};
const lock=async<T>(_name:string,action:()=>Promise<T>)=>action();
const row=():QuestionRecord=>({operationId:"original-K",versionId:"original-version",request:{page_path:"a.html",region_id:"runtime",body_ref:{object_id:"text",bytes_sha256:"a".repeat(64),byte_size:"5",media_type:"text/plain"},source_selection:{selections:[]}},textSHA:"a".repeat(64),textSize:5,runtimeGeneration:"original-runtime",dispatchReady:true});
const absent=()=>{throw new ApiError(404,"NOT_FOUND",false);};
const refused=(record:QuestionRecord)=>new ApiError(403,"FORBIDDEN",false,0,{question_admission:"refused_before_admission",operation_id:record.operationId,runtime_generation:record.runtimeGeneration,principal_id:"owner"});
const failed=(record:QuestionRecord)=>({operation_id:record.operationId,state:"failed",original_status:403,result_ref:null,job_id:null,result_metadata:{error:{code:"FORBIDDEN",retryable:false,details:refused(record).details}}});

test("T5QRCLIENT01 proven source-policy refusal requires explicit acknowledgement and retains frozen original identity before new intent",async()=>{
 const records=journal(),record=row();records.save(record);const original=structuredClone(record);let posts=0;
 const client={operation:async()=>posts===0?absent():failed(record),questionStart:async()=>{posts++;throw refused(record);}} as unknown as HaeryongClient;
 const submission=new QuestionSubmission(client,records,"library","owner",undefined,lock,()=>"original-runtime");
 await assert.rejects(submission.send(record,"",{}),ApiError);assert.equal(posts,1);assert.equal(questionUnresolved(records.read()[0]!),true);
 const pending=records.read()[0]!;assert.equal(pending.admissionRefusal!.acknowledged,false);assert.deepEqual(pending.request,original.request);
 await assert.rejects(submission.send(pending,"",{}),/접수되지/);assert.equal(posts,1);
 const acknowledged=await submission.acknowledgeRefusal(pending,()=>{});assert.equal(questionUnresolved(acknowledged),false);assert.equal(acknowledged.admissionRefusal!.acknowledged,true);assert.deepEqual(acknowledged.request,original.request);assert.equal(acknowledged.operationId,original.operationId);
 const next=submission.capture("original-version",{page_path:"a.html",region_id:null,source_selection:{selections:[]}},"new explicit question",()=>{});assert.notEqual(next.operationId,original.operationId);assert.equal(records.read().length,2);assert.deepEqual(records.read()[0],acknowledged);
});

test("T5QRCLIENT02 loose denial network lost202 receipt errors and mismatched proof never release unknown outcomes",async()=>{
 for(const failure of [new ApiError(403,"FORBIDDEN",false),new ApiError(404,"NOT_FOUND",false),new NetworkError(),new ApiError(409,"OPERATION_CONFLICT",false),new ApiError(403,"FORBIDDEN",false,0,{question_admission:"refused_before_admission",operation_id:"other",runtime_generation:"original-runtime",principal_id:"owner"})]){
  const records=journal(),record=row();records.save(record);const submission=new QuestionSubmission({operation:async()=>absent(),questionStart:async()=>{throw failure;}} as unknown as HaeryongClient,records,"library","owner",undefined,lock,()=>"original-runtime");
  await assert.rejects(submission.send(record,"",{}));assert.equal(records.read()[0]!.admissionRefusal,undefined);assert.equal(questionUnresolved(records.read()[0]!),true);await assert.rejects(submission.acknowledgeRefusal(record,()=>{}));
 }
 for(const outcome of ["permission","network","found","runtime","absent","malformed","wrong-status","wrong-key","completed"]){
  const records=journal(),record=row();records.save(record);let reads=0,runtime="original-runtime";
  const client={operation:async()=>{reads++;if(reads===1)return absent();if(outcome==="permission")throw new ApiError(403,"FORBIDDEN",false);if(outcome==="network")throw new NetworkError();if(outcome==="found")return {operation_id:record.operationId,state:"pending"};if(outcome==="absent"||outcome==="runtime")return absent();const receipt=failed(record);if(outcome==="malformed")receipt.result_metadata.error.details.principal_id="another-owner";if(outcome==="wrong-status")receipt.original_status=400;if(outcome==="wrong-key")receipt.operation_id="another-K";if(outcome==="completed")return {...receipt,state:"completed",original_status:202};return receipt;},questionStart:async()=>{if(outcome==="runtime")runtime="changed";throw refused(record);}} as unknown as HaeryongClient;
  const submission=new QuestionSubmission(client,records,"library","owner",undefined,lock,()=>runtime);await assert.rejects(submission.send(record,"",{}));assert.equal(records.read()[0]!.admissionRefusal,undefined);assert.equal(questionUnresolved(records.read()[0]!),true);
 }
});

test("T5QRCLIENT03 refusal acknowledgement rechecks current runtime owner receipt and journal before releasing its fence",async()=>{
 for(const outcome of ["permission","found","runtime","guard","save","absent"]){
  const records=journal(),record=row();record.admissionRefusal={status:403,code:"FORBIDDEN",runtimeGeneration:"original-runtime",acknowledged:false};records.save(record);const before=JSON.stringify(records.read());
  const client={operation:async()=>{if(outcome==="permission")throw new ApiError(403,"FORBIDDEN",false);if(outcome==="found")return {operation_id:record.operationId,state:"pending"};if(outcome==="absent")return absent();return failed(record);}} as unknown as HaeryongClient;
  const submission=new QuestionSubmission(client,outcome==="save"?{read:records.read,save:()=>{throw Error("storage denied");}}:records,"library","owner",undefined,lock,()=>outcome==="runtime"?"changed":"original-runtime");
  await assert.rejects(submission.acknowledgeRefusal(record,()=>{if(outcome==="guard")throw Error("retired authority");}));assert.equal(JSON.stringify(records.read()),before);
 }
});

const held=<T>()=>{let resolve!:(value:T)=>void;let reject!:(cause:unknown)=>void;const promise=new Promise<T>((yes,no)=>{resolve=yes;reject=no;});return {promise,resolve,reject};};
const question=(version:string)=>({question_id:"q-"+version,version_id:version}) as QuestionView;
test("T5QHISTORY01 deferred nonempty success failure retry and empty remain separate selected-version states",async()=>{
 const first=held<ListPage<QuestionView>>();let calls=0;const history=new QuestionHistory({questionList:async()=>{calls++;if(calls===1)return first.promise;if(calls===2)throw new NetworkError();return {items:[],next_cursor:null};}} as unknown as HaeryongClient);
 const read=history.load("v1",()=>{});assert.equal(history.state.phase,"loading");assert.deepEqual(history.state.items,[]);first.resolve({items:[question("v1")],next_cursor:null});await read;assert.equal(history.state.phase,"ready");assert.equal(history.state.items.length,1);
 await history.load("v1",()=>{});assert.equal(history.state.phase,"error");assert.ok(history.state.error instanceof NetworkError);await history.load("v1",()=>{});assert.equal(history.state.phase,"ready");assert.deepEqual(history.state.items,[]);
});
test("T5QHISTORY02 selection replacement and authority loss retire held responses before saved rows can reappear",async()=>{
 const old=held<ListPage<QuestionView>>(),fresh=held<ListPage<QuestionView>>();let calls=0,allowed=true;
 const history=new QuestionHistory({questionList:async()=>++calls===1?old.promise:fresh.promise} as unknown as HaeryongClient);
 const a=history.load("A",()=>{}),b=history.load("B",()=>{assert.ok(allowed);});old.resolve({items:[question("A")],next_cursor:null});await a;assert.equal(history.state.version,"B");assert.equal(history.state.phase,"loading");allowed=false;fresh.resolve({items:[question("B")],next_cursor:null});await b;assert.equal(history.state.phase,"idle");assert.deepEqual(history.state.items,[]);
 const late=held<ListPage<QuestionView>>();const second=new QuestionHistory({questionList:async()=>late.promise} as unknown as HaeryongClient);const pending=second.load("A",()=>{});second.invalidate();late.resolve({items:[question("A")],next_cursor:null});await pending;assert.equal(second.state.phase,"idle");assert.deepEqual(second.state.items,[]);
});

test("T5QRCLIENT04 malformed refusal journal fields fail closed instead of releasing an unknown original request",()=>{
 for(const proof of [{status:403,code:"FORBIDDEN",runtimeGeneration:"original-runtime",acknowledged:"true"},{status:403,code:"FORBIDDEN",runtimeGeneration:"other-runtime",acknowledged:true},{status:404,code:"FORBIDDEN",runtimeGeneration:"original-runtime",acknowledged:true},{status:403,code:"OTHER",runtimeGeneration:"original-runtime",acknowledged:true}]){
  const record={...row(),admissionRefusal:proof};const key="haeryong.questions.v1:owner:"+record.operationId,raw=JSON.stringify(record);const storage={length:1,key:()=>key,getItem:()=>raw,setItem:()=>{throw Error("must not rewrite malformed history");}} as unknown as Storage;assert.throws(()=>new BrowserQuestionJournal("owner",storage).read(),JournalReadError);assert.equal(questionUnresolved(record as QuestionRecord),true);
 }
});

test("T5QHISTORY03 current denial during a later list page clears protected rows while retaining honest error state",async()=>{
 let calls=0;const history=new QuestionHistory({questionList:async()=>{if(++calls===1)return {items:[question("v")],next_cursor:"next"};throw new ApiError(403,"FORBIDDEN",false);}} as unknown as HaeryongClient);await history.load("v",()=>{});assert.equal(history.state.items.length,1);await history.load("v",()=>{},true);assert.equal(history.state.phase,"error");assert.deepEqual(history.state.items,[]);assert.equal(history.state.nextCursor,null);
});

test("T5QRCLIENT05 lost refusal reply recovers the same terminal receipt before missing-body preparation or POST and still requires acknowledgement",async()=>{
 for(const scenario of ["lost-reply","missing-body-prefix"]){
  const records=journal();let record!:QuestionRecord,posts=0,reads=0,serverFailed=scenario==="missing-body-prefix";
  const client={operation:async()=>{reads++;return serverFailed?failed(record):absent();},questionStart:async()=>{posts++;serverFailed=true;throw new NetworkError();}} as unknown as HaeryongClient;
  const submission=new QuestionSubmission(client,records,"library","owner",undefined,lock,()=>"original-runtime");
  if(scenario==="lost-reply"){record=row();records.save(record);}else record=submission.capture("original-version",{page_path:"a.html",region_id:"runtime"},"original lost-reply question",()=>{});
  const original=structuredClone(record);
  if(scenario==="lost-reply"){await assert.rejects(submission.send(record,"",{}),NetworkError);assert.equal(questionUnresolved(records.read()[0]!),true);assert.equal(records.read()[0]!.admissionRefusal,undefined);}
  await assert.rejects(submission.send(record,"",{}),ApiError);assert.equal(posts,scenario==="lost-reply"?1:0);assert.equal(reads,scenario==="lost-reply"?3:2);
  const recovered=records.read()[0]!;assert.equal(recovered.dispatchReady,true);assert.equal(recovered.admissionRefusal!.acknowledged,false);assert.equal(questionUnresolved(recovered),true);assert.deepEqual(recovered.request,original.request);assert.equal(recovered.textSHA,original.textSHA);assert.equal(recovered.upload,undefined);
  const acknowledged=await submission.acknowledgeRefusal(recovered,()=>{});assert.equal(posts,scenario==="lost-reply"?1:0);assert.equal(reads,scenario==="lost-reply"?4:3);assert.equal(questionUnresolved(acknowledged),false);assert.deepEqual(acknowledged.request,original.request);assert.equal(acknowledged.operationId,original.operationId);
 }
 for(const failure of [new NetworkError(),new ApiError(403,"FORBIDDEN",false)]){
  const records=journal();let record!:QuestionRecord,reads=0,posts=0;
  const client={operation:async()=>{if(++reads===1)return failed(record);throw failure;},questionStart:async()=>{posts++;throw Error("must not POST");}} as unknown as HaeryongClient;
  const submission=new QuestionSubmission(client,records,"library","owner",undefined,lock,()=>"original-runtime");record=submission.capture("original-version",{page_path:"a.html",region_id:null},"original prefix question",()=>{});const original=structuredClone(record);
  await assert.rejects(submission.send(record,"",{}));const saved=records.read()[0]!;assert.equal(saved.dispatchReady,true);assert.equal(saved.admissionRefusal,undefined);assert.equal(questionUnresolved(saved),true);assert.deepEqual(saved.request,original.request);assert.equal(saved.operationId,original.operationId);assert.equal(saved.textSHA,original.textSHA);assert.equal(posts,0);assert.equal(reads,2);await assert.rejects(submission.acknowledgeRefusal(saved,()=>{}));
 }
});
