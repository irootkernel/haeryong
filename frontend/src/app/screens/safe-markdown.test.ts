import {test} from "node:test";
import assert from "node:assert/strict";
import {safeNoteLink,noteInline,noteMarkdown} from "./safe-markdown.ts";
test("T-005-02-03 hostile notes stay text and executable links are rejected",()=>{
 for(const url of ["javascript:alert(1)","JaVaScRiPt:run","%6aavascript:run","java%0ascript:run","&#106;avascript:run","data:text/html,script","vbscript:run","//unknown/frame","https:\\evil"]){assert.equal(safeNoteLink(url),null,url);}
 const raw='<script>window.noteExecuted=true</script><img src=x onerror=run()><svg onload=run()>';
 assert.deepEqual(noteMarkdown(raw),[{kind:"paragraph",text:raw}]);
 assert.ok(noteInline('[bad](javascript:run)').every(t=>t.kind!=="link"));
 for(const url of ["https://example.test/a","/documents/pinned","#part","../page.html","mailto:owner@example.test"]){assert.equal(safeNoteLink(url),url);}
 assert.deepEqual(noteMarkdown("# Heading\n\n- one\n- two\n\n```\n<script>inert code</script>\n```"),[{kind:"heading",level:1,text:"Heading"},{kind:"list",items:["one","two"]},{kind:"code",text:"<script>inert code</script>"}]);
});
