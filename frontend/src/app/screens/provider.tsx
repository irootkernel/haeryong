import { useEffect, useRef, useState } from "react";
import { navigate } from "../router.tsx";
import type { ShellController } from "../../client/controller.ts";
import type { ProviderState, ProviderStatus } from "../../client/types.ts";

const labels: Record<ProviderState, string> = {
  ready: "연결 확인됨", auth_required: "호스트에서 지원되는 Codex 구독 로그인을 확인하세요",
  quota_unavailable: "연결은 확인했으나 사용량 정보가 없습니다", quota_exhausted: "구독 사용량이 소진되었습니다",
  unsupported_version: "검증된 Codex 버전이 아닙니다", source_unavailable: "요청한 소스를 사용할 수 없습니다",
  paused: "제공자 실행이 일시 중지되었습니다", provider_error: "제공자 연결을 확인할 수 없습니다",
};

const reasons: Record<string, string> = {
  unsupported_provider: "현재 호스트의 제공자 또는 연결 주소는 지원되지 않습니다",
  mode_unavailable: "읽기 전용 실행 모드를 확인할 수 없습니다",
  usage_not_allowed: "일반 구독 사용이 현재 허용되지 않습니다. 소진 여부는 확인되지 않았습니다",
  unsupported_authentication: "현재 로그인 방식은 지원되지 않습니다",
  login_required: "호스트에서 Codex 구독 로그인을 확인하세요",
  quota_unknown: "구독 사용량 정보가 제공되지 않았습니다",
  config_unavailable: "선택한 작업 폴더의 실행 설정을 확인할 수 없습니다",
  config_read_failed: "호스트 실행 설정을 읽을 수 없습니다",
  account_read_failed: "호스트 로그인 상태를 읽을 수 없습니다",
  catalog_read_failed: "모델 목록을 확인할 수 없습니다",
  cleanup_unknown: "로컬 제공자 연결의 종료를 확인할 수 없습니다",
  initialize_failed: "로컬 제공자 연결을 시작할 수 없습니다",
  transport_unavailable: "로컬 제공자 연결을 사용할 수 없습니다",
};
export function ProviderScreen({ controller }: { controller: ShellController }) {
  const busy = useRef(true);
  const [status, setStatus] = useState<ProviderStatus | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [refresh, setRefresh] = useState(0);
  const [loading, setLoading] = useState(true);
  const authority = `${controller.state.capabilities?.library_id}:${controller.state.capabilities?.runtime_generation}:${controller.state.session?.principal_id}`;
  useEffect(() => {
    let active = true;
    busy.current = true;
    setLoading(true);
    setStatus(null);
    setError(null);
    void controller.client.providerStatus().then(value => { if (active) setStatus(value); })
      .catch(cause => { if (active) setError(cause instanceof Error ? cause.message : String(cause)); })
      .finally(() => { if (active) { busy.current = false; setLoading(false); } });
    return () => { active = false; };
  }, [controller, authority, refresh]);
  return <section className="screen" data-provider-screen>
    <h1>AI 제공자</h1>
    <p>호스트의 Codex 구독 연결 상태입니다. 서비스 기능 지원과 계정 준비는 별개입니다.</p>
    <p>생성 {controller.state.capabilities?.features.generation?"지원됨":"제공되지 않음"} · 질문 {controller.state.capabilities?.features.questions?"지원됨":"제공되지 않음"}. 연결 확인은 요청의 완료나 원본 접근을 보장하지 않습니다.</p>
    <button type="button" aria-disabled={loading} onClick={() => { if (busy.current) return; busy.current = true; setRefresh(value => value + 1); }}>제공자 상태 새로 고침</button>
    <p role="status" aria-live="polite" aria-atomic="true">{loading ? "제공자 상태 확인 중…" : error ? "제공자 상태 확인 실패" : status ? labels[status.state] : "연결 상태를 확인할 수 없습니다"}</p>
    {error && <p role="alert">{error}</p>}
    {status && <div data-provider-status>
      <h2>Codex</h2><p data-provider-state>{labels[status.state]}</p>
      {status.reason && <p data-provider-reason>{reasons[status.reason] ?? "호스트에서 제공자 상태를 확인하세요"}</p>}
      <dl><dt>로그인 방식</dt><dd>{status.authentication_mode === "chatgpt" ? "ChatGPT 구독" : status.authentication_mode === "apiKey" ? "API 키 로그인 (지원되지 않음)" : "확인되지 않음"}</dd>
        <dt>일반 구독 사용</dt><dd>{status.ordinary_usage_allowed == null ? "사용량 정보 없음" : status.ordinary_usage_allowed ? "현재 허용됨" : "현재 허용되지 않음"}</dd>
        <dt>실행 모드</dt><dd>{status.read_only ? "읽기 전용 · 추가 승인 없음" : "확인되지 않음"}</dd>
        {status.model && <><dt>현재 모델</dt><dd>{status.model}{status.effort ? ` · ${status.effort}` : ""}</dd></>}
      </dl>
      <p>모델 목록과 사용량 조회는 실제 요청의 접근 권한이나 완료를 보장하지 않습니다. 사용량 정보가 없을 때 한도를 추정하지 않습니다.</p>
    </div>}
    <p>로그인·계정·결제 설정은 호스트의 Codex에서 관리합니다. 연결이 없어도 기존 문서 읽기와 Review는 계속 사용할 수 있습니다.</p>
    <nav aria-label="다른 설정" className="form-row">
      {[["/settings/general", "일반 설정"], ["/settings/sessions", "세션 및 보안 정책"], ["/connect", "연결 및 장치"], ["/settings/projects", "프로젝트 및 소스"], ["/settings/backup", "백업 및 내보내기"], ["/settings/storage", "저장 공간"]].map(([path, label]) =>
        <a className="action-link" key={path} href={path} onClick={event => {
          if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
          event.preventDefault();
          navigate(path);
        }}>{label}</a>)}
    </nav>
  </section>;
}
