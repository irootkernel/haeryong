// VersionHeader: exact directory version identity and any supplied release/
// hash/provenance. Missing metadata renders unknown/unverified — it is never
// a failure to open the result, and the existing version ID display in the
// reader header stays untouched. Each supplied SOURCE identity carries an
// accessible full-identity disclosure (S04 short hash with expandable full
// identity: a snapshot's exact 64-hex digest is never truncated away); the
// payload/manifest/supplied digests are content digests, not source
// identity, and stay abbreviated.
import { useState } from "react";
import type { VersionView } from "../../client/types.ts";
import type { SuppliedMetadata, SuppliedSourceRecord } from "./evidence-view.ts";

const basis = (value: string | undefined): string => {
  switch (value) {
    case "evidence_recorded": return "증거 기록됨(정확성 보증 아님)";
    case "unverified": return "알 수 없음/검증되지 않음";
    default: return value ?? "알 수 없음/검증되지 않음";
  }
};

function SourceProvenance({ source }: { source: SuppliedSourceRecord }) {
  const [open, setOpen] = useState(false);
  const identity = source.kind === "git"
    ? [source.repo_id, source.object_format, source.exact_revision].filter((part) => typeof part === "string" && part !== "").join(" · ")
    : typeof source.snapshot_sha256 === "string" ? `snapshot ${source.snapshot_sha256.slice(0, 12)}…` : "";
  return <li>
    <span className="muted-code">{source.id}</span>{" "}
    {typeof source.release_label === "string" && source.release_label !== "" ? `(${source.release_label}) ` : ""}
    {source.kind === "git" ? "git" : "snapshot"}
    {typeof source.retention_mode === "string" ? ` · 보존 ${source.retention_mode === "selected_snapshot" ? "선택 스냅샷" : source.retention_mode}` : ""}
    {identity !== "" ? ` · ${identity}` : " · 정확 개정 없음"}
    <button type="button" className="link" aria-expanded={open} onClick={() => setOpen((value) => !value)}>전체 소스 신원 보기/숨기기</button>
    {open && <p className="muted-code source-full-identity">
      {source.kind === "git"
        ? `repo ${source.repo_id ?? "없음"} · ${source.object_format ?? "형식 없음"} · 개정 ${source.exact_revision ?? "없음"}${typeof source.release_label === "string" && source.release_label !== "" ? ` · 라벨 ${source.release_label}` : ""}`
        : `snapshot_sha256 ${source.snapshot_sha256 ?? "없음"}${typeof source.asset_path === "string" ? ` · 자산 ${source.asset_path}` : ""}${typeof source.bytes_sha256 === "string" ? ` · 바이트 ${source.bytes_sha256}` : ""}`}
    </p>}
  </li>;
}

export function VersionHeader(props: { version: VersionView | null; pagePath: string | null; metadata: SuppliedMetadata | null }) {
  const version = props.version;
  const sources = props.metadata?.sources ?? [];
  return <section className="version-header" aria-label="버전 및 출처 정보">
    <p className="muted-code">
      버전 {version ? `#${version.sequence}` : "…(현재 버전 정보 확인 중)"}{version?.base_version_id ? ` · 기반 ${version.base_version_id.slice(0, 12)}…` : ""}
      {props.pagePath !== null ? ` · 현재 페이지 ${props.pagePath}` : " · 페이지 없음"}
      {` · 출처 기준 ${version ? basis(version.source_basis) : "…"}`}
    </p>
    <p className="muted-code">
      payload {version ? version.payload_sha256.slice(0, 12) : "…"}… · manifest {version ? version.manifest_sha256.slice(0, 12) : "…"}…
      {version?.supplied_metadata_sha256 ? ` · supplied metadata ${version.supplied_metadata_sha256.slice(0, 12)}…` : ""}
    </p>
    {sources.length > 0 ? <ul className="version-sources" aria-label="공급된 소스 정체성"><SourceProvenance source={sources[0]} />{sources.slice(1).map((source) => <SourceProvenance key={source.id} source={source} />)}</ul>
      : <p>공급된 소스/출처 메타데이터 없음 — 알 수 없음/검증되지 않음.</p>}
  </section>;
}
