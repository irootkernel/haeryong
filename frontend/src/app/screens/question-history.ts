import {ApiError,StaleResponseError} from "../../client/types.ts";
import type {HaeryongClient,ListPage,QuestionView} from "../../client/types.ts";

export interface QuestionHistoryState {
 version:string;phase:"idle"|"loading"|"ready"|"error";items:QuestionView[];
 nextCursor:string|null;error:unknown|null;
}
/** Owns the selected-version list and retires held replies on selection/access loss. */
export class QuestionHistory {
 readonly client:HaeryongClient;
 state:QuestionHistoryState={version:"",phase:"idle",items:[],nextCursor:null,error:null};
 #era=0;#listeners=new Set<()=>void>();
 constructor(client:HaeryongClient){this.client=client;}
 subscribe(listener:()=>void){this.#listeners.add(listener);return()=>{this.#listeners.delete(listener);};}
 #set(state:QuestionHistoryState){this.state=state;for(const listener of this.#listeners)listener();}
 invalidate(){this.#era++;this.#set({version:"",phase:"idle",items:[],nextCursor:null,error:null});}
 async load(version:string,guard:()=>void,more=false,reader:HaeryongClient=this.client,rethrow=false){
  const previous=this.state;
  if(more&&(previous.version!==version||previous.phase!=="ready"||!previous.nextCursor))return;
  const era=++this.#era,cursor=more?previous.nextCursor!:undefined;
  this.#set({version,phase:"loading",items:more?previous.items:[],nextCursor:cursor??null,error:null});
  try{
   guard();const page:ListPage<QuestionView>=await reader.questionList(version,cursor);
   if(era!==this.#era){if(rethrow)throw new StaleResponseError();return;}guard();
   this.#set({version,phase:"ready",items:more?[...previous.items,...page.items]:page.items,nextCursor:page.next_cursor,error:null});
  }catch(error){
   if(era!==this.#era){if(rethrow)throw new StaleResponseError();return;}
   // Current authority failure removes protected rows. A failed enumeration
   // never asserts that the selected version has an empty saved history.
   try{guard();}catch(cause){this.invalidate();if(rethrow)throw cause;return;}
   const denied=error instanceof ApiError&&[401,403,404].includes(error.status);
   this.#set({version,phase:"error",items:more&&!denied?previous.items:[],nextCursor:denied?null:cursor??null,error});
   if(rethrow)throw error;
  }
 }
}

export async function revalidateInspectedQuestion(
 reader:Pick<HaeryongClient,"questionGet">,
 questionId:string|null,
 canApply:()=>boolean,
 applySaved:(question:QuestionView)=>void,
 clearSaved:()=>void,
 onError?:(error:unknown)=>void,
 rethrow=false,
):Promise<void>{
 if(!questionId)return;
 try{
  const fresh=await reader.questionGet(questionId);
  if(!canApply())return;
  applySaved(fresh);
 }catch(error){
  if(!canApply())return;
  if(error instanceof ApiError&&[401,403,404].includes(error.status)){
   clearSaved();
   return;
  }
  onError?.(error);
  if(rethrow)throw error;
 }
}
