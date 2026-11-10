package sqlite

// Each migration is immutable: persisted history verifies these exact bytes.
var migrations = []string{
	`CREATE TABLE schema_migrations(revision INTEGER PRIMARY KEY,checksum TEXT NOT NULL,applied_at TEXT NOT NULL);
CREATE TABLE libraries(singleton INTEGER PRIMARY KEY CHECK(singleton=1),library_id TEXT NOT NULL UNIQUE,profile_id TEXT NOT NULL,schema_revision INTEGER NOT NULL,runtime_generation TEXT NOT NULL,stream_epoch TEXT NOT NULL,auth_epoch TEXT NOT NULL,unfiled_project_id TEXT NOT NULL);
CREATE TABLE projects(project_id TEXT PRIMARY KEY,name TEXT NOT NULL,lifecycle TEXT NOT NULL CHECK(lifecycle IN ('active','archived')),revision INTEGER NOT NULL CHECK(revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1));
CREATE TABLE config_revisions(config_revision INTEGER PRIMARY KEY CHECK(config_revision>=1),validated_json BLOB NOT NULL CHECK(json_valid(validated_json)),actor TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE resource_revisions(resource_kind TEXT NOT NULL,resource_id TEXT NOT NULL,revision INTEGER NOT NULL CHECK(revision>=1),value BLOB,PRIMARY KEY(resource_kind,resource_id));
CREATE TABLE events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,stream_epoch TEXT NOT NULL,resource_kind TEXT NOT NULL,resource_id TEXT NOT NULL,kind TEXT NOT NULL,revision INTEGER NOT NULL,body BLOB);
`,
	`CREATE TABLE operation_receipts(principal_id TEXT NOT NULL,operation_id TEXT NOT NULL,request_fingerprint TEXT NOT NULL,original_request BLOB NOT NULL,normalization_context BLOB NOT NULL,state TEXT NOT NULL CHECK(state IN ('accepted','completed','failed','reconciling')),original_status INTEGER,result BLOB,CHECK((state IN ('accepted','reconciling') AND original_status IS NULL AND result IS NULL) OR (state IN ('completed','failed') AND original_status IS NOT NULL AND result IS NOT NULL)),PRIMARY KEY(principal_id,operation_id));
CREATE TABLE content_objects(content_hash TEXT PRIMARY KEY,byte_size INTEGER NOT NULL CHECK(byte_size>=0),state TEXT NOT NULL CHECK(state IN ('ready','deleting')),gc_token TEXT);
CREATE TABLE object_pins(pin_id TEXT PRIMARY KEY,owner_kind TEXT NOT NULL,owner_id TEXT NOT NULL,content_hash TEXT NOT NULL REFERENCES content_objects(content_hash));
CREATE TABLE object_references(owner_kind TEXT NOT NULL,owner_id TEXT NOT NULL,content_hash TEXT NOT NULL REFERENCES content_objects(content_hash),PRIMARY KEY(owner_kind,owner_id,content_hash));
CREATE TABLE content_sets(content_set_id TEXT PRIMARY KEY,payload_sha256 TEXT NOT NULL,sealed INTEGER NOT NULL CHECK(sealed IN (0,1)));
CREATE TABLE version_entries(content_set_id TEXT NOT NULL REFERENCES content_sets(content_set_id),relative_path TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('file','directory','symlink')),content_hash TEXT REFERENCES content_objects(content_hash),byte_size INTEGER NOT NULL CHECK(byte_size>=0),exact_link_text TEXT,PRIMARY KEY(content_set_id,relative_path),CHECK((kind='file' AND content_hash IS NOT NULL) OR (kind!='file' AND content_hash IS NULL)));
CREATE TABLE documents(document_id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(project_id),title TEXT NOT NULL,lifecycle TEXT NOT NULL CHECK(lifecycle IN ('active','archived','trash','purged')),latest_version_id TEXT REFERENCES document_versions(version_id) DEFERRABLE INITIALLY DEFERRED,revision INTEGER NOT NULL CHECK(revision>=1));
CREATE TABLE document_versions(version_id TEXT PRIMARY KEY,document_id TEXT NOT NULL REFERENCES documents(document_id),sequence INTEGER CHECK(sequence>=1),base_version_id TEXT REFERENCES document_versions(version_id),content_set_id TEXT NOT NULL REFERENCES content_sets(content_set_id),title_snapshot TEXT NOT NULL,default_page TEXT,viewer_profile TEXT NOT NULL,source_basis TEXT NOT NULL,metadata BLOB,UNIQUE(document_id,sequence));
CREATE TABLE version_pages(version_id TEXT NOT NULL REFERENCES document_versions(version_id),page_path TEXT NOT NULL,metadata BLOB,PRIMARY KEY(version_id,page_path));
CREATE TABLE regions(version_id TEXT NOT NULL,page_path TEXT NOT NULL,region_id TEXT NOT NULL,metadata BLOB,PRIMARY KEY(version_id,page_path,region_id),FOREIGN KEY(version_id,page_path) REFERENCES version_pages(version_id,page_path));
CREATE TABLE version_sources(version_id TEXT NOT NULL REFERENCES document_versions(version_id),source_id TEXT NOT NULL,metadata BLOB,PRIMARY KEY(version_id,source_id));
CREATE TABLE evidence(version_id TEXT NOT NULL,evidence_id TEXT NOT NULL,source_id TEXT NOT NULL,metadata BLOB,PRIMARY KEY(version_id,evidence_id),FOREIGN KEY(version_id,source_id) REFERENCES version_sources(version_id,source_id));
CREATE TABLE region_evidence(version_id TEXT NOT NULL,page_path TEXT NOT NULL,region_id TEXT NOT NULL,evidence_id TEXT NOT NULL,PRIMARY KEY(version_id,page_path,region_id,evidence_id),FOREIGN KEY(version_id,page_path,region_id) REFERENCES regions(version_id,page_path,region_id),FOREIGN KEY(version_id,evidence_id) REFERENCES evidence(version_id,evidence_id));
CREATE TABLE jobs(job_id TEXT PRIMARY KEY,kind TEXT NOT NULL,state TEXT NOT NULL CHECK(state IN ('queued','running','interrupted','reconciling','paused','cancelling','cancelled','completed','failed')),attempt_generation INTEGER NOT NULL CHECK(attempt_generation>=1),cancel_requested INTEGER NOT NULL DEFAULT 0 CHECK(cancel_requested IN (0,1)),parent_id TEXT REFERENCES jobs(job_id),upload_id TEXT REFERENCES uploads(upload_id),error TEXT NOT NULL DEFAULT '',scope BLOB NOT NULL,config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revisions BLOB NOT NULL,deadline TEXT);
CREATE TABLE uploads(upload_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,purpose TEXT NOT NULL,input_mode TEXT NOT NULL,selection_name TEXT NOT NULL,target BLOB NOT NULL,state TEXT NOT NULL CHECK(state IN ('open','sealing','sealed','abandoned')),checkpoint BLOB NOT NULL,seal_job_id TEXT REFERENCES jobs(job_id),sealed_content_set_id TEXT REFERENCES content_sets(content_set_id),error TEXT NOT NULL DEFAULT '');
CREATE TABLE upload_ranges(upload_id TEXT NOT NULL REFERENCES uploads(upload_id),relative_path TEXT NOT NULL,range_offset INTEGER NOT NULL CHECK(range_offset>=0),byte_size INTEGER NOT NULL CHECK(byte_size>=0),content_hash TEXT NOT NULL,PRIMARY KEY(upload_id,relative_path,range_offset));
CREATE INDEX pins_by_hash ON object_pins(content_hash);
CREATE INDEX refs_by_hash ON object_references(content_hash);
CREATE INDEX jobs_by_state ON jobs(state);
`,
	`ALTER TABLE jobs ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1);
CREATE TABLE auth_owner(singleton INTEGER PRIMARY KEY CHECK(singleton=1),principal_id TEXT NOT NULL UNIQUE);
CREATE TABLE device_sessions(session_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL REFERENCES auth_owner(principal_id),library_id TEXT NOT NULL,auth_epoch TEXT NOT NULL,token_hash TEXT NOT NULL UNIQUE,device_label TEXT NOT NULL,origin TEXT NOT NULL,created_at INTEGER NOT NULL,last_seen_at INTEGER NOT NULL,revoked INTEGER NOT NULL CHECK(revoked IN (0,1)),revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1));
CREATE TABLE pairings(pairing_id TEXT PRIMARY KEY,device_label TEXT NOT NULL,origin TEXT NOT NULL,created_at INTEGER NOT NULL,expires_at INTEGER NOT NULL,state TEXT NOT NULL CHECK(state IN ('pending','approved','rejected','expired')),revision INTEGER NOT NULL CHECK(revision>=1),secret_hash TEXT NOT NULL,code_hash TEXT NOT NULL,client_hash TEXT NOT NULL,auth_epoch TEXT NOT NULL,poll_after INTEGER NOT NULL,consumed INTEGER NOT NULL CHECK(consumed IN (0,1)));
CREATE INDEX pairings_client_created ON pairings(client_hash,created_at);
`,
	`ALTER TABLE uploads ADD COLUMN originating_device_id TEXT;
ALTER TABLE uploads ADD COLUMN created_at TEXT NOT NULL DEFAULT '';
ALTER TABLE uploads ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE uploads ADD COLUMN revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE uploads ADD COLUMN seal_operation_id TEXT;
ALTER TABLE uploads ADD COLUMN sealed_object_hash TEXT;
ALTER TABLE jobs ADD COLUMN result BLOB;
ALTER TABLE document_versions ADD COLUMN payload_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE document_versions ADD COLUMN manifest_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE document_versions ADD COLUMN supplied_metadata_hash TEXT;
CREATE TABLE upload_entries(upload_id TEXT NOT NULL REFERENCES uploads(upload_id),relative_path TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('file','directory','symlink')),link_text TEXT NOT NULL DEFAULT '',expected_size INTEGER,final INTEGER NOT NULL CHECK(final IN (0,1)),PRIMARY KEY(upload_id,relative_path));
CREATE TRIGGER upload_revision AFTER UPDATE OF state,checkpoint,error,seal_job_id,sealed_content_set_id,sealed_object_hash ON uploads
WHEN old.state IS NOT new.state OR old.checkpoint IS NOT new.checkpoint OR old.error IS NOT new.error OR old.seal_job_id IS NOT new.seal_job_id OR old.sealed_content_set_id IS NOT new.sealed_content_set_id OR old.sealed_object_hash IS NOT new.sealed_object_hash
BEGIN UPDATE uploads SET revision=old.revision+1,updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE upload_id=new.upload_id; END;
CREATE TABLE import_intents(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE TABLE metadata_indexes(index_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,metadata_hash TEXT NOT NULL REFERENCES content_objects(content_hash),content_set_id TEXT NOT NULL REFERENCES content_sets(content_set_id),job_id TEXT REFERENCES jobs(job_id),complete INTEGER NOT NULL DEFAULT 0 CHECK(complete IN(0,1)));
CREATE TABLE import_metadata(job_id TEXT NOT NULL REFERENCES metadata_indexes(index_id) ON UPDATE CASCADE ON DELETE CASCADE,kind TEXT NOT NULL,item_id TEXT NOT NULL,value BLOB NOT NULL,PRIMARY KEY(job_id,kind,item_id));
`, `ALTER TABLE projects ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
ALTER TABLE documents ADD COLUMN updated_at TEXT NOT NULL DEFAULT '';
UPDATE projects SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now');
UPDATE documents SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now');
CREATE TRIGGER project_created AFTER INSERT ON projects WHEN new.updated_at='' BEGIN UPDATE projects SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE project_id=new.project_id; END;
CREATE TRIGGER document_created AFTER INSERT ON documents WHEN new.updated_at='' BEGIN UPDATE documents SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE document_id=new.document_id; END;
CREATE TRIGGER project_updated AFTER UPDATE OF name,lifecycle,revision,permissions_revision ON projects
WHEN old.name IS NOT new.name OR old.lifecycle IS NOT new.lifecycle OR old.revision IS NOT new.revision OR old.permissions_revision IS NOT new.permissions_revision
BEGIN UPDATE projects SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE project_id=new.project_id; END;
CREATE TRIGGER document_updated AFTER UPDATE OF title,lifecycle,latest_version_id,revision ON documents
WHEN old.title IS NOT new.title OR old.lifecycle IS NOT new.lifecycle OR old.latest_version_id IS NOT new.latest_version_id OR old.revision IS NOT new.revision
BEGIN UPDATE documents SET updated_at=strftime('%Y-%m-%dT%H:%M:%fZ','now') WHERE document_id=new.document_id; END;
CREATE TABLE workflow_principals(principal_id TEXT PRIMARY KEY,binding_id TEXT NOT NULL UNIQUE,active INTEGER NOT NULL CHECK(active IN(0,1)),scope BLOB NOT NULL CHECK(json_valid(scope)),revision INTEGER NOT NULL CHECK(revision>=1));
CREATE TABLE scoped_credentials(credential_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL REFERENCES workflow_principals(principal_id),token_hash TEXT NOT NULL UNIQUE,auth_epoch TEXT NOT NULL,scope BLOB NOT NULL CHECK(json_valid(scope)),permissions BLOB NOT NULL CHECK(json_valid(permissions)),config_revision INTEGER NOT NULL CHECK(config_revision>=1),created_at INTEGER NOT NULL,expires_at INTEGER,revoked INTEGER NOT NULL CHECK(revoked IN(0,1)),revision INTEGER NOT NULL CHECK(revision>=1));
CREATE TABLE configuration_plans(plan_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,base_revision INTEGER NOT NULL CHECK(base_revision>=1),expires_at TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)));
`,
	`CREATE TABLE contexts(context_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,version_id TEXT NOT NULL,page_path TEXT,render_instance_id TEXT,origin_session_id TEXT NOT NULL,media_type TEXT NOT NULL CHECK(media_type IN ('application/json','text/plain')),content_hash TEXT NOT NULL REFERENCES content_objects(content_hash),byte_size INTEGER NOT NULL CHECK(byte_size>=0),created_at TEXT NOT NULL);
`,
	`CREATE TABLE captured_target_sets(target_set_id TEXT PRIMARY KEY,project_id TEXT NOT NULL,requested_selection BLOB NOT NULL CHECK(json_valid(requested_selection)),targets_json BLOB NOT NULL CHECK(json_valid(targets_json)),captured_at TEXT NOT NULL);
CREATE INDEX captures_by_project ON captured_target_sets(project_id,captured_at);
CREATE TABLE captured_reads(read_id TEXT PRIMARY KEY,target_set_id TEXT NOT NULL REFERENCES captured_target_sets(target_set_id),basis TEXT NOT NULL CHECK(basis IN ('project_source')),source_json BLOB NOT NULL CHECK(json_valid(source_json)),location_json BLOB NOT NULL CHECK(json_valid(location_json)),bytes_sha256 TEXT,config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),created_at TEXT NOT NULL);
CREATE TABLE citation_verifications(verification_id TEXT PRIMARY KEY,principal_id TEXT NOT NULL,version_id TEXT NOT NULL,evidence_id TEXT NOT NULL,read_id TEXT REFERENCES captured_reads(read_id),target_set_id TEXT REFERENCES captured_target_sets(target_set_id),source_json BLOB NOT NULL CHECK(json_valid(source_json)),location_json BLOB NOT NULL CHECK(json_valid(location_json)),bytes_sha256 TEXT,bytes_origin TEXT CHECK(bytes_origin IN ('approved_source','retained_snapshot')),outcome TEXT NOT NULL CHECK(outcome IN ('captured','matched','mismatch','unavailable')),expected_bytes_sha256 TEXT,expected_excerpt BLOB,expected_excerpt_ref TEXT,config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),checked_at TEXT NOT NULL,verified_at TEXT,reason TEXT NOT NULL DEFAULT '',operation_id TEXT NOT NULL,CHECK((verified_at IS NULL) OR (verified_at IS NOT NULL AND outcome IN ('captured','matched'))));
CREATE INDEX verifications_by_evidence ON citation_verifications(version_id,evidence_id,checked_at,verification_id);
CREATE UNIQUE INDEX verifications_by_operation ON citation_verifications(principal_id,operation_id);
CREATE TABLE observation_runs(run_id TEXT PRIMARY KEY,version_id TEXT NOT NULL,principal_id TEXT NOT NULL,operation_id TEXT NOT NULL,requested_selection BLOB NOT NULL CHECK(json_valid(requested_selection)),scope_json BLOB NOT NULL CHECK(json_valid(scope_json)),target_set_id TEXT REFERENCES captured_target_sets(target_set_id),created_at TEXT NOT NULL);
CREATE UNIQUE INDEX observation_runs_by_operation ON observation_runs(principal_id,operation_id);
CREATE INDEX observation_runs_by_version ON observation_runs(version_id,created_at,run_id);
CREATE TABLE source_observations(observation_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES observation_runs(run_id),version_id TEXT NOT NULL,target_set_id TEXT REFERENCES captured_target_sets(target_set_id),source_id TEXT NOT NULL,repo_id TEXT NOT NULL DEFAULT '',base_revision TEXT NOT NULL DEFAULT '',observed_revision TEXT,availability TEXT NOT NULL CHECK(availability IN ('available','unavailable','denied')),classification TEXT NOT NULL CHECK(classification IN ('direct','indirect','unchanged','unavailable','denied')),changed_paths_json BLOB,dependency_scope_json BLOB,reason TEXT NOT NULL DEFAULT '',config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),observed_at TEXT NOT NULL,operation_id TEXT NOT NULL);
CREATE INDEX observations_by_source ON source_observations(source_id,observed_at);
CREATE INDEX observations_by_time ON source_observations(observed_at,observation_id);
CREATE INDEX observations_by_run ON source_observations(run_id);
CREATE TABLE reassessment_triggers(trigger_id TEXT PRIMARY KEY,run_id TEXT NOT NULL REFERENCES observation_runs(run_id),version_id TEXT NOT NULL,target_set_id TEXT NOT NULL REFERENCES captured_target_sets(target_set_id),source_id TEXT NOT NULL,anchor_kind TEXT NOT NULL CHECK(anchor_kind IN ('version','page','region')),page_path TEXT NOT NULL DEFAULT '',region_id TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL,changed_paths_json BLOB,observation_id TEXT NOT NULL,observed_at TEXT NOT NULL,created_at TEXT NOT NULL,CHECK((anchor_kind='version' AND page_path='' AND region_id='') OR (anchor_kind='page' AND page_path!='' AND region_id='') OR (anchor_kind='region' AND page_path!='' AND region_id!='')));
CREATE INDEX triggers_by_target ON reassessment_triggers(version_id,target_set_id,anchor_kind,page_path,region_id);
CREATE TABLE check_intents(intent_id TEXT PRIMARY KEY,operation_id TEXT NOT NULL,principal_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('verification','observation')),canonical_target BLOB NOT NULL CHECK(json_valid(canonical_target)),validated_body BLOB,expectation_upload TEXT,config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),state TEXT NOT NULL CHECK(state IN ('open','completed','failed')),failure_reason TEXT NOT NULL DEFAULT '',created_at TEXT NOT NULL,resolved_at TEXT);
CREATE INDEX check_intents_state ON check_intents(state);
CREATE UNIQUE INDEX check_intents_by_operation ON check_intents(principal_id,operation_id);
CREATE TABLE coverage_sets(coverage_set_id TEXT PRIMARY KEY,version_id TEXT NOT NULL,target_set_id TEXT REFERENCES captured_target_sets(target_set_id),run_id TEXT REFERENCES observation_runs(run_id),scope_json BLOB NOT NULL CHECK(json_valid(scope_json)),unit_kind TEXT NOT NULL CHECK(unit_kind IN ('version','page','region')),captured_at TEXT NOT NULL,origin TEXT NOT NULL,CHECK(target_set_id IS NOT NULL OR origin='source_observation'),CHECK(origin<>'source_observation' OR run_id IS NOT NULL));
CREATE INDEX coverage_sets_by_run ON coverage_sets(run_id);
CREATE INDEX coverage_sets_by_version ON coverage_sets(version_id,target_set_id);
CREATE TABLE required_units(coverage_set_id TEXT NOT NULL REFERENCES coverage_sets(coverage_set_id),kind TEXT NOT NULL CHECK(kind IN ('version','page','region')),page_path TEXT NOT NULL DEFAULT '',region_id TEXT NOT NULL DEFAULT '',reason TEXT NOT NULL DEFAULT '',target_set_id TEXT,PRIMARY KEY(coverage_set_id,kind,page_path,region_id),CHECK((kind='version' AND page_path='' AND region_id='') OR (kind='page' AND page_path!='' AND region_id='') OR (kind='region' AND page_path!='' AND region_id!='')));
CREATE TABLE assessments(assessment_id TEXT PRIMARY KEY,version_id TEXT NOT NULL,page_path TEXT NOT NULL DEFAULT '',region_id TEXT NOT NULL DEFAULT '',target_set_id TEXT NOT NULL,coverage_set_id TEXT NOT NULL,state TEXT NOT NULL,scope_json BLOB NOT NULL CHECK(json_valid(scope_json)),evidence_json BLOB,config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),origin TEXT NOT NULL,created_at TEXT NOT NULL);
CREATE TABLE assessment_units(coverage_set_id TEXT NOT NULL,assessment_id TEXT NOT NULL,target_set_id TEXT NOT NULL,kind TEXT NOT NULL CHECK(kind IN ('version','page','region')),page_path TEXT NOT NULL DEFAULT '',region_id TEXT NOT NULL DEFAULT '',outcome TEXT NOT NULL CHECK(outcome IN ('assessed','partial','blocked','not_checked')),discrepancy TEXT,reason TEXT,PRIMARY KEY(coverage_set_id,assessment_id,kind,page_path,region_id));
`,
	`CREATE TABLE derived_work(
 job_id TEXT PRIMARY KEY REFERENCES jobs(job_id),
 version_id TEXT NOT NULL REFERENCES document_versions(version_id),
 work_kind TEXT NOT NULL CHECK(work_kind<>''),
 extractor_revision INTEGER NOT NULL CHECK(extractor_revision>=1),
 after_path TEXT NOT NULL DEFAULT '',
 UNIQUE(version_id,work_kind,extractor_revision)
);
CREATE TABLE search_indexes(
 version_id TEXT PRIMARY KEY REFERENCES document_versions(version_id),
 job_id TEXT REFERENCES jobs(job_id),
 extractor_revision INTEGER NOT NULL CHECK(extractor_revision>=1),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>=1),
 discovery_complete INTEGER NOT NULL DEFAULT 0 CHECK(discovery_complete IN(0,1)),
 state TEXT NOT NULL CHECK(state<>''),
 reason TEXT NOT NULL DEFAULT ''
);
CREATE TABLE search_units(
 unit_id INTEGER PRIMARY KEY,
 version_id TEXT NOT NULL REFERENCES document_versions(version_id),
 kind TEXT NOT NULL CHECK(kind<>''),
 page_path TEXT NOT NULL DEFAULT '', region_id TEXT NOT NULL DEFAULT '',
 source_id TEXT NOT NULL DEFAULT '', title TEXT NOT NULL DEFAULT '',
 input_hash TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL CHECK(state<>''),
 body_state TEXT NOT NULL CHECK(body_state<>''),
 anchor_state TEXT NOT NULL CHECK(anchor_state<>''),
 reason TEXT NOT NULL DEFAULT '',
 UNIQUE(version_id,kind,page_path,region_id,source_id),
 CHECK(region_id='' OR page_path!='')
);
CREATE INDEX search_units_by_version ON search_units(version_id,kind,page_path);
CREATE TABLE search_chunks(
 chunk_id INTEGER PRIMARY KEY,
 unit_id INTEGER NOT NULL REFERENCES search_units(unit_id) ON DELETE CASCADE,
 part TEXT NOT NULL CHECK(part<>''),
 ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 title TEXT NOT NULL, path TEXT NOT NULL, terms TEXT NOT NULL, body TEXT NOT NULL,
 display_body TEXT NOT NULL,
 UNIQUE(unit_id,part,ordinal)
);
CREATE VIRTUAL TABLE search_words USING fts5(
 title,path,terms,body,content='search_chunks',content_rowid='chunk_id',tokenize='unicode61'
);
CREATE VIRTUAL TABLE search_grams USING fts5(
 title,path,terms,body,content='search_chunks',content_rowid='chunk_id',tokenize='trigram'
);
CREATE TRIGGER search_chunks_insert AFTER INSERT ON search_chunks BEGIN
 INSERT INTO search_words(rowid,title,path,terms,body) VALUES(new.chunk_id,new.title,new.path,new.terms,new.body);
 INSERT INTO search_grams(rowid,title,path,terms,body) VALUES(new.chunk_id,new.title,new.path,new.terms,new.body);
END;
CREATE TRIGGER search_chunks_delete AFTER DELETE ON search_chunks BEGIN
 INSERT INTO search_words(search_words,rowid,title,path,terms,body) VALUES('delete',old.chunk_id,old.title,old.path,old.terms,old.body);
 INSERT INTO search_grams(search_grams,rowid,title,path,terms,body) VALUES('delete',old.chunk_id,old.title,old.path,old.terms,old.body);
END;
CREATE TRIGGER search_chunks_update AFTER UPDATE ON search_chunks BEGIN
 INSERT INTO search_words(search_words,rowid,title,path,terms,body) VALUES('delete',old.chunk_id,old.title,old.path,old.terms,old.body);
 INSERT INTO search_grams(search_grams,rowid,title,path,terms,body) VALUES('delete',old.chunk_id,old.title,old.path,old.terms,old.body);
 INSERT INTO search_words(rowid,title,path,terms,body) VALUES(new.chunk_id,new.title,new.path,new.terms,new.body);
 INSERT INTO search_grams(rowid,title,path,terms,body) VALUES(new.chunk_id,new.title,new.path,new.terms,new.body);
END;
CREATE TABLE page_anchors(
 version_id TEXT NOT NULL, page_path TEXT NOT NULL,
 anchor_id TEXT NOT NULL CHECK(anchor_id!=''),
 locator_kind TEXT NOT NULL CHECK(locator_kind<>''),
 occurrences INTEGER NOT NULL CHECK(occurrences>=1),
 label TEXT NOT NULL DEFAULT '', text_sha256 TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(version_id,page_path,anchor_id),
 FOREIGN KEY(version_id,page_path) REFERENCES version_pages(version_id,page_path)
);
`,
	`CREATE TABLE reference_materializations(
 from_version_id TEXT PRIMARY KEY REFERENCES document_versions(version_id),
 manifest_sha256 TEXT NOT NULL CHECK(length(manifest_sha256)=64),
 reference_revision INTEGER NOT NULL CHECK(reference_revision>=1),
 state TEXT NOT NULL CHECK(state<>''),
 job_id TEXT NOT NULL REFERENCES jobs(job_id),
 revision INTEGER NOT NULL CHECK(revision>=1)
);
CREATE TABLE authored_references(
 from_version_id TEXT NOT NULL,
 metadata_reference_id TEXT NOT NULL CHECK(metadata_reference_id<>''),
 from_page_path TEXT,
 from_region_id TEXT,
 target_document_id TEXT NOT NULL,
 target_version_id TEXT NOT NULL,
 target_page_path TEXT,
 target_region_id TEXT,
 page_query TEXT NOT NULL DEFAULT '',
 page_fragment TEXT NOT NULL DEFAULT '',
 relation TEXT,
 label TEXT,
 authored_json BLOB NOT NULL,
 CHECK(from_region_id IS NULL OR from_page_path IS NOT NULL),
 CHECK(target_region_id IS NULL OR target_page_path IS NOT NULL),
 PRIMARY KEY(from_version_id,metadata_reference_id),
 FOREIGN KEY(from_version_id) REFERENCES reference_materializations(from_version_id)
);
CREATE INDEX authored_references_target
 ON authored_references(target_version_id,from_version_id,metadata_reference_id);
CREATE TABLE authored_edges(
 edge_id TEXT PRIMARY KEY,
 from_version_id TEXT NOT NULL,
 metadata_reference_id TEXT NOT NULL,
 from_page_path TEXT,
 from_region_id TEXT,
 target_project_id TEXT NOT NULL,
 target_document_id TEXT NOT NULL,
 target_version_id TEXT NOT NULL,
 target_page_path TEXT,
 target_region_id TEXT,
 from_page_sha256 TEXT,
 from_anchor_sha256 TEXT,
 target_page_sha256 TEXT,
 target_anchor_sha256 TEXT,
 anchor_revision INTEGER NOT NULL CHECK(anchor_revision>=1),
 UNIQUE(from_version_id,metadata_reference_id),
 CHECK(from_region_id IS NULL OR from_page_path IS NOT NULL),
 CHECK(target_region_id IS NULL OR target_page_path IS NOT NULL),
 FOREIGN KEY(from_version_id,metadata_reference_id)
  REFERENCES authored_references(from_version_id,metadata_reference_id)
);
CREATE INDEX authored_edges_target
 ON authored_edges(target_version_id,target_page_path,target_region_id,
                  from_version_id,metadata_reference_id);
`,
	`CREATE TABLE review_events(sequence INTEGER PRIMARY KEY AUTOINCREMENT,event_id TEXT NOT NULL UNIQUE,owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),version_id TEXT NOT NULL REFERENCES document_versions(version_id),action TEXT NOT NULL CHECK(action IN ('start','complete','reopen','clear')),old_state TEXT NOT NULL CHECK(old_state IN ('unreviewed','reviewing','reviewed')),new_state TEXT NOT NULL CHECK(new_state IN ('unreviewed','reviewing','reviewed')),actor_session TEXT NOT NULL,operation_id TEXT NOT NULL,created_at TEXT NOT NULL,UNIQUE(owner_id,operation_id));
CREATE INDEX review_events_target ON review_events(owner_id,version_id,sequence);
CREATE TABLE review_states(owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),version_id TEXT NOT NULL REFERENCES document_versions(version_id),state TEXT NOT NULL CHECK(state IN ('unreviewed','reviewing','reviewed')),reviewed_at TEXT,revision INTEGER NOT NULL CHECK(revision>=1),event_sequence INTEGER NOT NULL REFERENCES review_events(sequence),PRIMARY KEY(owner_id,version_id),CHECK((state='reviewed' AND reviewed_at IS NOT NULL) OR (state!='reviewed' AND reviewed_at IS NULL)));
CREATE INDEX review_states_owner_state ON review_states(owner_id,state,event_sequence);
`,
	`CREATE TABLE annotations(annotation_id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),document_id TEXT NOT NULL REFERENCES documents(document_id),original_version_id TEXT NOT NULL REFERENCES document_versions(version_id),version_id TEXT NOT NULL REFERENCES document_versions(version_id),page_path TEXT,region_id TEXT,revision INTEGER NOT NULL CHECK(revision>=1),deleted_at TEXT,value BLOB NOT NULL,CHECK(region_id IS NULL OR page_path IS NOT NULL));
CREATE INDEX annotations_owner_version ON annotations(owner_id,version_id,annotation_id);
CREATE TABLE annotation_history(annotation_id TEXT NOT NULL REFERENCES annotations(annotation_id),revision INTEGER NOT NULL CHECK(revision>=1),value BLOB NOT NULL,PRIMARY KEY(annotation_id,revision));
CREATE TABLE revisit_flags(owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),document_id TEXT NOT NULL REFERENCES documents(document_id),flag INTEGER NOT NULL CHECK(flag IN (0,1)),revision INTEGER NOT NULL CHECK(revision>=1),PRIMARY KEY(owner_id,document_id));
CREATE INDEX revisit_flags_owner_flag ON revisit_flags(owner_id,flag,document_id);
`,
	`ALTER TABLE documents ADD COLUMN previous_lifecycle TEXT CHECK(previous_lifecycle IS NULL OR previous_lifecycle IN ('active','archived'));
ALTER TABLE documents ADD COLUMN trashed_at TEXT;
ALTER TABLE documents ADD COLUMN trash_generation INTEGER NOT NULL DEFAULT 0 CHECK(trash_generation>=0);
CREATE TABLE lifecycle_plans(plan_id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),document_id TEXT NOT NULL REFERENCES documents(document_id),operation TEXT NOT NULL CHECK(operation IN ('archive','trash','restore')),state TEXT NOT NULL CHECK(state IN ('open','committed')),expected_revision INTEGER NOT NULL CHECK(expected_revision>=1),config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),expires_at TEXT NOT NULL,digest TEXT NOT NULL,value BLOB NOT NULL);
CREATE TABLE lifecycle_plan_items(plan_id TEXT NOT NULL REFERENCES lifecycle_plans(plan_id),sequence INTEGER NOT NULL CHECK(sequence>=1),value BLOB NOT NULL,PRIMARY KEY(plan_id,sequence));
CREATE INDEX lifecycle_trash ON documents(lifecycle,document_id);
`,
	`ALTER TABLE lifecycle_plan_items RENAME TO purge_saved_items;
ALTER TABLE lifecycle_plans RENAME TO purge_saved_plans;
CREATE TABLE lifecycle_plans(plan_id TEXT PRIMARY KEY,owner_id TEXT NOT NULL REFERENCES auth_owner(principal_id),document_id TEXT NOT NULL REFERENCES documents(document_id),operation TEXT NOT NULL CHECK(operation IN ('archive','trash','restore','purge')),state TEXT NOT NULL CHECK(state IN ('open','committed')),expected_revision INTEGER NOT NULL CHECK(expected_revision>=1),config_revision INTEGER NOT NULL CHECK(config_revision>=1),permissions_revision INTEGER NOT NULL CHECK(permissions_revision>=1),expires_at TEXT NOT NULL,digest TEXT NOT NULL,value BLOB NOT NULL);
CREATE TABLE lifecycle_plan_items(plan_id TEXT NOT NULL REFERENCES lifecycle_plans(plan_id),sequence INTEGER NOT NULL CHECK(sequence>=1),value BLOB NOT NULL,PRIMARY KEY(plan_id,sequence));
INSERT INTO lifecycle_plans SELECT * FROM purge_saved_plans;
INSERT INTO lifecycle_plan_items SELECT * FROM purge_saved_items;
DROP TABLE purge_saved_items;
DROP TABLE purge_saved_plans;
CREATE TABLE purged_versions(version_id TEXT PRIMARY KEY,document_id TEXT NOT NULL REFERENCES documents(document_id),deleted_at TEXT NOT NULL);
CREATE INDEX purged_versions_document ON purged_versions(document_id,version_id);
CREATE TABLE purge_intents(job_id TEXT PRIMARY KEY REFERENCES jobs(job_id),plan_id TEXT NOT NULL UNIQUE REFERENCES lifecycle_plans(plan_id),document_id TEXT NOT NULL UNIQUE REFERENCES documents(document_id),value BLOB NOT NULL CHECK(json_valid(value)));
CREATE TABLE purge_candidates(job_id TEXT NOT NULL REFERENCES purge_intents(job_id),content_hash TEXT NOT NULL,byte_size INTEGER NOT NULL CHECK(byte_size>=0),PRIMARY KEY(job_id,content_hash));
`,
	copyMigration,
	providerMigration,
	sourceCaptureMigration,
	providerTransportMigration,
	generationMigration,
	questionMigration,
	scanMigration,
	maintenanceMigration,
	freshnessMigration,
}
