import {useEffect,useRef} from "react";
import type {HaeryongClient} from "../../client/types.ts";

// Events are content-free hints. Coalesce them and fetch authenticated state;
// an in-flight owner action finishes before its view is refreshed.
export function useReviewInvalidation(client:HaeryongClient,enabled:boolean,refresh:()=>void,busy:()=>boolean=()=>false) {
  const callbacks=useRef({refresh,busy});callbacks.current={refresh,busy};
  useEffect(()=>{
    if(!enabled)return;
    const events=client.subscribeEvents();let timer:ReturnType<typeof setTimeout>|undefined,stopped=false;
    const run=()=>{timer=undefined;if(stopped)return;if(callbacks.current.busy()){timer=setTimeout(run,1000);return;}callbacks.current.refresh();};
    const unsubscribe=events.onInvalidation?.(()=>{if(timer===undefined)timer=setTimeout(run,1000);});
    return()=>{stopped=true;clearTimeout(timer);unsubscribe?.();events.close();};
  },[client,enabled]);
}
