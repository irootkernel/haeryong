import type { DecisionResult, HaeryongClient, UploadCheckpoint } from "./types.ts";

export interface ContextBinding {
  version_id: string;
  page_path: string | null;
  render_instance_id: string | null;
}

export interface ContextReceipt {
  context_ref: string;
  bytes_sha256: string;
  media_type: string;
  created_at: string;
}

// Shared closed transport facet. Only the trusted host chooses this target;
// a child page receives neither the client nor an authenticated fetch proxy.
export interface ContextClient extends Pick<HaeryongClient, "putUploadEntry" | "uploadDetail" | "uploadRanges" | "completeUpload"> {
  createContextUpload(binding: ContextBinding, operationId: string): Promise<{ upload_id: string; checkpoint: UploadCheckpoint }>;
  consumeContext(binding: ContextBinding, uploadId: string, operationId: string): Promise<DecisionResult>;
  /**
   * Fresh-binding proof for ready/echo: proves, in one server transaction,
   * that the current session is the record's exact originating session and
   * that the persisted tuple and object digest match the expected values
   * from the receipt. The expected digest is always explicit; the proof
   * never returns or buffers context bytes. The ordinary authorized
   * recovery read stays a separate, session-independent HTTP GET.
   */
  verifyContextFresh(contextRef: string, binding: ContextBinding, bytesSha256: string): Promise<void>;
}
