import type { DocumentView, ReviewView } from "../../client/types.ts";
export function reviewTarget(document:DocumentView,kind:"reviewing"|"updated_since_review"):string|null {
  return kind==="reviewing"?document.continue_review_version_id??null:document.updated_since_review===true?document.latest_version_id:null;
}
/** Version rows use exact resource reads, never a document's latest-version summary. */
export function exactReview(views:Record<string,ReviewView>,version:string):ReviewView|null {const value=views[version];return value?.version_id===version?value:null;}
