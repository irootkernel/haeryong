import {useEffect,useRef} from "react";
import type {ShellController} from "../../client/controller.ts";

// All visible resources share the controller's authenticated stream. Multiple
// panels/tabs must not consume a new long-lived HTTP connection per widget.
export function useReviewInvalidation(controller:ShellController,enabled:boolean,refresh:()=>void,busy:()=>boolean=()=>false) {
  const callbacks=useRef({refresh,busy});callbacks.current={refresh,busy};
  useEffect(()=>{
    if(!enabled)return;
    let timer:ReturnType<typeof setTimeout>|undefined,stopped=false;
    const run=()=>{timer=undefined;if(stopped)return;if(callbacks.current.busy()){timer=setTimeout(run,1000);return;}callbacks.current.refresh();};
    const off=controller.subscribeInvalidation(()=>{if(timer===undefined)timer=setTimeout(run,1000);});
    return()=>{stopped=true;clearTimeout(timer);off();};
  },[controller,enabled]);
}
