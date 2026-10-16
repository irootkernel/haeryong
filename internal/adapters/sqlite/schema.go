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
`,
}
