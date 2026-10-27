/** Presentation chunks, never a limit on saved context. */
export interface NoteContextText {parts:string[];firstEnd:number;lastStart:number;length:number}
const hostTask=()=>new Promise<void>(resolve=>setTimeout(resolve,0));
export async function prepareNoteContext(text:string,guard:()=>void,yieldTask:()=>Promise<void>=hostTask):Promise<NoteContextText>{
 guard();
 if(typeof Intl.Segmenter!=="function")throw Error("이 환경에서 문맥 텍스트를 준비할 수 없습니다.");
 const segmenter=new Intl.Segmenter(undefined,{granularity:"grapheme"});
 const parts:string[]=[];let start=0,end=0,firstEnd=0,lastStart=0,batch=0;
 const release=async()=>{guard();await yieldTask();guard();};
 await release();
 for(const item of segmenter.segment(text)){
  const next=item.index+item.segment.length;
  if(end>start&&next-start>4096){parts.push(text.slice(start,end));start=end;}
  if(!firstEnd)firstEnd=next;
  lastStart=item.index;end=next;
  if(end-batch>=32768){await release();batch=end;}
 }
 guard();if(end>start)parts.push(text.slice(start,end));
 return {parts,firstEnd,lastStart,length:text.length};
}

/** Resolve offsets from the mounted tree, without assuming React's node shape. */
export function noteContextRange(pre:HTMLElement,start:number,end:number):Range{
 const walker=document.createTreeWalker(pre,NodeFilter.SHOW_TEXT);
 const range=document.createRange();let offset=0,found=false,node:Node|null;
 while((node=walker.nextNode())){
  const text=node as Text,next=offset+text.length;
  if(!found&&start<=next){range.setStart(text,start-offset);found=true;}
  if(found&&end<=next){range.setEnd(text,end-offset);return range;}
  offset=next;
 }
 if(start===0&&end===0&&!pre.textContent){range.selectNodeContents(pre);range.collapse(true);return range;}
 throw Error("문맥의 저장된 위치를 확인할 수 없습니다.");
}

export function moveNoteContext(pre:HTMLElement,text:NoteContextText,end:boolean):void{
 if(!text.length){pre.focus({preventScroll:true});return;}
 const position=end?text.length:0,caret=noteContextRange(pre,position,position);
 let rect=[...caret.getClientRects()].find(r=>r.height>0);
 if(!rect){
  const fragments=[...noteContextRange(pre,end?text.lastStart:0,end?text.length:text.firstEnd).getClientRects()];
  if(fragments.length===1&&fragments[0]!.height>0)rect=fragments[0];
 }
 if(!rect)throw Error("문맥의 처음 또는 끝 위치를 표시할 수 없습니다.");
 const box=pre.getBoundingClientRect(),left=box.left+pre.clientLeft,top=box.top+pre.clientTop,right=left+pre.clientWidth,bottom=top+pre.clientHeight;
 pre.scrollLeft+=rect.left<left?rect.left-left:rect.right>right?rect.right-right:0;
 pre.scrollTop+=rect.top<top?rect.top-top:rect.bottom>bottom?rect.bottom-bottom:0;
 pre.focus({preventScroll:true});
}
