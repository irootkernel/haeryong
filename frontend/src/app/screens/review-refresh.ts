import {useEffect,useRef} from "react";
import type {InvalidationEvent,ShellController} from "../../client/controller.ts";

// All visible resources share the controller's authenticated stream. Multiple
// panels/tabs must not consume a new long-lived HTTP connection per widget.
export function useReviewInvalidation(
  controller:ShellController,
  enabled:boolean,
  refresh:(event?:InvalidationEvent)=>void|Promise<void>,
  busy:()=>boolean=()=>false,
) {
  const callbacks=useRef({refresh,busy});callbacks.current={refresh,busy};
  useEffect(()=>{
    if(!enabled)return;
    let timer:ReturnType<typeof setTimeout>|undefined;
    let stopped=false;
    let refreshEpoch=0;
    const cancelWaiters=new Set<()=>void>();
    const waitIdle=(delay:number):Promise<boolean>=>new Promise<boolean>((resolve)=>{
      const t=setTimeout(()=>{
        cancelWaiters.delete(cancel);
        resolve(!stopped);
      },delay);
      const cancel=()=>{
        clearTimeout(t);
        cancelWaiters.delete(cancel);
        resolve(false);
      };
      cancelWaiters.add(cancel);
    });
    const run=async(ev:InvalidationEvent|undefined,epoch:number):Promise<void>=>{
      while(!stopped&&epoch===refreshEpoch&&callbacks.current.busy()){
        const ok=await waitIdle(ev?.resync?50:250);
        if(!ok||stopped||epoch!==refreshEpoch)return;
      }
      if(stopped||epoch!==refreshEpoch)return;
      if(!ev?.resync)timer=undefined;
      const effective=ev?{...ev,readGeneration:controller.readGeneration}:undefined;
      await callbacks.current.refresh(effective);
    };
    const off=controller.subscribeInvalidation((ev)=>{
      if(stopped)return;
      if(ev?.resync){
        refreshEpoch++;
        if(timer!==undefined){
          clearTimeout(timer);
          timer=undefined;
        }
        return run(ev,refreshEpoch);
      }
      if(timer===undefined){
        const epoch=refreshEpoch;
        timer=setTimeout(()=>{void run(ev,epoch).catch(()=>{});},250);
      }
    });
    return()=>{
      stopped=true;
      if(timer!==undefined){
        clearTimeout(timer);
        timer=undefined;
      }
      for(const cancel of [...cancelWaiters])cancel();
      off();
    };
  },[controller,enabled]);
}
