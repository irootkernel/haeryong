// Home screen: connection-aware summary with truthful priority sections.
// The review/freshness APIs do not exist yet, so Continue/Updated/Revisit/
// Needs-attention render honest no-data states and never infer priority from
// updated_at ordering. Local delivered jobs/receipts stay reachable through
// the transfer surfaces.
import type { ShellController } from "../../client/controller.ts";
import { homeSectionsUnavailable } from "./library-data.ts";
import { navigate } from "../router.tsx";

interface HomeScreenProps {
  controller: ShellController;
}

const SECTIONS: { key: string; title: string }[] = [
  { key: "continue", title: "이어 읽기" },
  { key: "updated", title: "최근 업데이트" },
  { key: "revisit", title: "다시 볼 문서" },
  { key: "attention", title: "주의 필요" },
];

export function HomeScreen({ controller }: HomeScreenProps) {
  const state = controller.state;
  const unavailable = homeSectionsUnavailable();
  return (
    <section className="panel">
      <h1>홈</h1>
      <p className="hint" data-testid="home-session">
        {state.session ? `${state.session.device_label}로 연결됨 · 라이브러리 ${state.capabilities?.library_id ?? ""}` : "연결 정보를 확인하는 중입니다."}
      </p>
      {SECTIONS.map((section) => (
        <div key={section.key} className="home-section" data-testid={`home-section-${section.key}`}>
          <h2>{section.title}</h2>
          <p className="hint">{unavailable.reason}</p>
        </div>
      ))}
      <div className="form-row">
        <button type="button" className="primary" onClick={() => navigate("/library")}>
          서재 열기
        </button>
        <button type="button" onClick={() => navigate("/settings/storage")}>
          저장소·업로드 확인
        </button>
      </div>
    </section>
  );
}
