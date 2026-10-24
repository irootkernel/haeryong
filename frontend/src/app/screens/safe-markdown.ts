export type Inline = {kind:"text"|"code"|"strong"|"em";text:string}|{kind:"link";text:string;href:string};
export type MarkdownBlock = {kind:"paragraph"|"heading"|"code";text:string;level?:number}|{kind:"list";items:string[]};

// React renders every token as text. Raw HTML and unsupported syntax never enter an HTML parser.
export function safeNoteLink(raw:string):string|null {
  let decoded=raw.trim(),previous="";
  while(decoded!==previous){previous=decoded;try{decoded=decodeURIComponent(decoded);}catch{return null;}}
  const probe=decoded.replace(/&#(?:x([0-9a-f]+)|(\d+));?/gi,(_,hex,decimal)=>String.fromCodePoint(Math.min(0x10ffff,Number.parseInt(hex??decimal,hex?16:10)||0))).replace(/&colon;/gi,":").replace(/[\u0000-\u0020\u007f-\u009f]/g,"");
  if(probe.startsWith("//")||probe.includes("\\"))return null;
  const scheme=/^([^/?#]*):/.exec(probe);
  if(scheme&&!/^(https?|mailto)$/i.test(scheme[1]!))return null;
  return raw;
}
export function noteInline(text:string):Inline[]{
 const out:Inline[]=[];const pattern=/`([^`]+)`|\*\*([^*]+)\*\*|\*([^*]+)\*|\[([^\]]+)\]\(([^\s)]+)\)/g;let at=0;
 for(const m of text.matchAll(pattern)){if(m.index!>at)out.push({kind:"text",text:text.slice(at,m.index)});if(m[1])out.push({kind:"code",text:m[1]});else if(m[2])out.push({kind:"strong",text:m[2]});else if(m[3])out.push({kind:"em",text:m[3]});else{const href=safeNoteLink(m[5]!);out.push(href?{kind:"link",text:m[4]!,href}:{kind:"text",text:m[0]});}at=m.index!+m[0].length;}if(at<text.length)out.push({kind:"text",text:text.slice(at)});return out;
}
export function noteMarkdown(text:string):MarkdownBlock[]{
 const blocks:MarkdownBlock[]=[];const lines=text.split(/\r?\n/);let paragraph:string[]=[],code:string[]|null=null;
 const flush=()=>{if(paragraph.length){blocks.push({kind:"paragraph",text:paragraph.join("\n")});paragraph=[];}};
 for(const line of lines){if(line.startsWith("```")){flush();if(code){blocks.push({kind:"code",text:code.join("\n")});code=null;}else code=[];continue;}if(code){code.push(line);continue;}if(!line.trim()){flush();continue;}const heading=/^(#{1,6}) (.*)$/.exec(line),item=/^[-*] (.*)$/.exec(line);if(heading){flush();blocks.push({kind:"heading",level:heading[1]!.length,text:heading[2]!});}else if(item){flush();const last=blocks.at(-1);if(last?.kind==="list")last.items.push(item[1]!);else blocks.push({kind:"list",items:[item[1]!]});}else paragraph.push(line);}
 if(code)blocks.push({kind:"code",text:code.join("\n")});flush();return blocks;
}
