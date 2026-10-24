import {test} from "node:test";
import assert from "node:assert/strict";
import {exactReview,reviewTarget} from "./review-summary.ts";
import {defaultPair} from "./versions-data.ts";
import type {DocumentView,ReviewView,VersionView} from "../../client/types.ts";
const versions=[{version_id:"v3",sequence:3},{version_id:"v4",sequence:4}] as VersionView[];
test("N4 Versions shows exact v3 Reviewed and v4 Unreviewed without latest-summary substitution",()=>{
 const v3={version_id:"v3",value:{state:"reviewed",reviewed_at:"time"}} as ReviewView;
 const v4={version_id:"v4",value:{state:"unreviewed",reviewed_at:null}} as ReviewView;
 assert.equal(exactReview({v3,v4},"v3")?.value.state,"reviewed");assert.equal(exactReview({v3,v4},"v4")?.value.state,"unreviewed");assert.equal(exactReview({v3:v4},"v3"),null);
 assert.deepEqual(defaultPair(versions,null,null,"v4","v3"),["v3","v4"]);
 assert.deepEqual(defaultPair([{version_id:"v1",sequence:1} as VersionView,...versions],null,null,"v4","v1"),["v1","v4"]);
 assert.deepEqual(defaultPair(versions,"v4",null,"v4","v3"),["v4","v4"]);
 assert.throws(()=>defaultPair(versions,"foreign",null,"v4","v3"));
});
test("Home continues an older Reviewing version and Updated targets exact latest",()=>{
 const doc={latest_version_id:"v4",continue_review_version_id:"v3",last_reviewed_version_id:"v3",updated_since_review:true} as DocumentView;
 assert.equal(reviewTarget(doc,"reviewing"),"v3");assert.equal(reviewTarget(doc,"updated_since_review"),"v4");assert.equal(reviewTarget({...doc,updated_since_review:false},"updated_since_review"),null);
});
