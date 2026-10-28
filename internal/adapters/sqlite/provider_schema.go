package sqlite

const providerMigration = `
CREATE TABLE provider_attempts(
 attempt_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL REFERENCES jobs(job_id),
 attempt_generation INTEGER NOT NULL CHECK(attempt_generation>=1),
 application_generation INTEGER NOT NULL CHECK(application_generation>=1),
 state TEXT NOT NULL CHECK(state IN ('prepared','dispatching','running','result_saved','failed','cancelled','outcome_unknown')),
 runtime_generation TEXT NOT NULL,
 restart_generation INTEGER NOT NULL DEFAULT 0,
 may_have_dispatched INTEGER NOT NULL DEFAULT 0 CHECK(may_have_dispatched IN (0,1)),
 request_sha256 TEXT NOT NULL,
 target_set_id TEXT REFERENCES captured_target_sets(target_set_id),
 provider_request_id TEXT NOT NULL DEFAULT '',
 provider_session_id TEXT NOT NULL DEFAULT '',
 result_hash TEXT,
 reason TEXT NOT NULL DEFAULT '',
 UNIQUE(job_id,attempt_generation)
);
CREATE INDEX provider_attempts_job ON provider_attempts(job_id,attempt_generation);
CREATE TABLE provider_retry_authorizations(job_id TEXT NOT NULL REFERENCES jobs(job_id),attempt_generation INTEGER NOT NULL,PRIMARY KEY(job_id,attempt_generation));
CREATE TABLE provider_reconcile_intents(job_id TEXT NOT NULL REFERENCES jobs(job_id),attempt_id TEXT NOT NULL,ledger_attempt_id TEXT REFERENCES provider_attempts(attempt_id),orphan_attempt_id TEXT REFERENCES provider_orphans(attempt_id),state TEXT NOT NULL CHECK(state IN ('pending','executing')),runtime_generation TEXT NOT NULL DEFAULT '',PRIMARY KEY(job_id,attempt_id),CHECK((ledger_attempt_id IS NOT NULL AND ledger_attempt_id=attempt_id AND orphan_attempt_id IS NULL) OR (orphan_attempt_id IS NOT NULL AND orphan_attempt_id=attempt_id AND ledger_attempt_id IS NULL)));
CREATE TABLE provider_linked_retry_authorizations(job_id TEXT NOT NULL REFERENCES jobs(job_id),prior_attempt_id TEXT NOT NULL,decision TEXT NOT NULL DEFAULT 'retry' CHECK(decision IN ('retry','reconcile_only')),PRIMARY KEY(job_id,prior_attempt_id,decision));
CREATE TABLE provider_orphans(attempt_id TEXT PRIMARY KEY,request_sha256 TEXT NOT NULL,state TEXT NOT NULL,value BLOB NOT NULL CHECK(json_valid(value)));
CREATE INDEX provider_orphans_request ON provider_orphans(request_sha256);
CREATE TABLE provider_inputs(
 attempt_id TEXT NOT NULL REFERENCES provider_attempts(attempt_id),
 read_id TEXT NOT NULL REFERENCES captured_reads(read_id),
 content_hash TEXT NOT NULL,
 input_origin TEXT CHECK(input_origin IN ('daemon_supplied','native_observed')),
 observed_at TEXT,
 CHECK((input_origin IS NULL AND observed_at IS NULL) OR (input_origin IS NOT NULL AND observed_at IS NOT NULL AND observed_at<>'')),
 PRIMARY KEY(attempt_id,read_id)
);
`
