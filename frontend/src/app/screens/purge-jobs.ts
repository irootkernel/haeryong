import {ApiError} from "../../client/types.ts";
import type {HaeryongClient,JobView} from "../../client/types.ts";
export const purgeJobsKey=(library:unknown,owner:unknown)=>"haeryong.purge-jobs.v1:"+JSON.stringify([library,owner]);
export type PurgeJobRow={id:string;kind:"job";job:JobView}|{id:string;kind:"unavailable"|"retry"};
export function rememberedPurgeJobs(storage:Pick<Storage,"getItem">,key:string):string[]{
 const ids:unknown=JSON.parse(storage.getItem(key)??"[]");
 if(!Array.isArray(ids)||!ids.every(id=>typeof id==="string"))throw Error("삭제 작업 기록을 확인하세요.");
 return [...new Set(ids)];
}
export function rememberPurgeJobId(storage:Pick<Storage,"getItem"|"setItem">,key:string,id:string):void{
 storage.setItem(key,JSON.stringify([...new Set([...rememberedPurgeJobs(storage,key),id])]));
}
export async function lookupPurgeJobs(client:Pick<HaeryongClient,"jobGet">,ids:string[]):Promise<PurgeJobRow[]>{
 return Promise.all(ids.map(async(id):Promise<PurgeJobRow>=>{
  try{return {id,kind:"job",job:await client.jobGet(id)};}
  catch(e){return {id,kind:e instanceof ApiError&&[403,404].includes(e.status)?"unavailable":"retry"};}
 }));
}
export function canDismissPurgeJob(row:PurgeJobRow):boolean{
 return row.kind==="unavailable"||row.kind==="job"&&["completed","failed","cancelled"].includes(row.job.state);
}
export function purgeJobsNeedPoll(rows:PurgeJobRow[]):boolean{
 return rows.some(row=>row.kind==="retry"||row.kind==="job"&&!canDismissPurgeJob(row));
}
// Called under the shared owner/library bucket lock. Merge against the latest
// list so dismissing one observed terminal/unavailable job keeps other work.
export function dismissPurgeJob(storage:Pick<Storage,"getItem"|"setItem">,key:string,row:PurgeJobRow):void{
 if(!canDismissPurgeJob(row))throw Error("진행 중인 삭제 작업 기록은 닫을 수 없습니다.");
 storage.setItem(key,JSON.stringify(rememberedPurgeJobs(storage,key).filter(id=>id!==row.id)));
}
