import { useEffect, useRef, useState } from "react";
import type { ShellController } from "../../client/controller.ts";
import { pinnedReaderURL } from "../routes.ts";
import { navigate } from "../router.tsx";
import { copiedLink, openVersions } from "./deep-links.ts";
import type { ReaderPosition } from "./reader-navigation.ts";

export function ReaderVersionActions(props: { controller: ShellController; documentId: string; versionId: string; page: string | null; query: string; fragment: string; region: string | null; owner: string; readPosition(): Promise<ReaderPosition> }) {
  const { controller } = props;
  const lifetime = useRef(0);
  useEffect(() => { lifetime.current++; return () => { lifetime.current++; }; }, []);
  const [link, setLink] = useState<{ url: string; localOnly: boolean } | null>(null);
  const [notice, setNotice] = useState("");
  const action = (work: (guard: () => void) => Promise<void>) => {
    const authority = controller.captureDispatchAuthority(), route = location.href, context = history.state?.haeryongReaderContext, generation = lifetime.current;
    const guard = () => {
      controller.requireVerifiedDispatch(authority);
      if (lifetime.current !== generation || location.href !== route || history.state?.haeryongReaderContext !== context) throw Error("읽기 선택이 변경되었습니다.");
    };
    void work(guard).catch(error => { try { guard(); setNotice(String(error instanceof Error ? error.message : error)); } catch { /* previous selection */ } });
  };
  return <>
    <button id="reader-copy-link" onClick={() => action(async guard => {
      guard(); const configuration = await controller.client.configurationView(); guard();
      const value = copiedLink(configuration, controller.client.publicOrigin, pinnedReaderURL(props.documentId, props.versionId, props.page, props.query, props.fragment, props.region));
      setLink(value);
      try { await navigator.clipboard.writeText(value.url); guard(); setNotice("링크를 복사했습니다."); }
      catch { guard(); setNotice("아래 링크를 선택해 복사하세요."); }
    })}>링크 복사</button>
    <button id="reader-versions" onClick={() => action(async guard => { guard(); const position = await props.readPosition(); guard(); openVersions(props.documentId, props.versionId, props.owner, position, pinnedReaderURL(props.documentId, props.versionId, props.page, props.query, props.fragment, props.region)); })}>버전 및 변경</button>
    <button id="reader-open-latest" onClick={() => action(async guard => { guard(); const doc = await controller.client.documentDetail(props.documentId); guard(); if (!doc.latest_version_id) throw Error("발행된 버전이 없습니다."); navigate(pinnedReaderURL(props.documentId, doc.latest_version_id)); })}>최신 버전 열기</button>
    {link && <label className="field">{link.localOnly ? "이 기기에서만 열리는 링크(local-only)" : "승인된 연결 링크"}<input aria-label="복사할 링크" readOnly value={link.url} onFocus={event => event.target.select()} /></label>}
    {notice && <p role="status">{notice}</p>}
  </>;
}
