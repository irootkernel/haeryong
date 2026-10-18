import { useEffect, useMemo, useRef, useState } from "react";
import type { ShellController } from "../client/controller.ts";
import type { ContextClient } from "../client/context-types.ts";
import type { PersonalTarget, PersonalOperations } from "../app/screens/personal-operations.ts";
import { BrowserContextJournal, ContextTransferHost, freshContextVerifier } from "./context-transfer.ts";
import { ViewerChannel } from "./channel.ts";
import { RenderSessionLease } from "./render-session.ts";

export interface ReaderLocation { page: string; query: string; fragment: string }
export interface ViewerProps {
  controller: ShellController;
  owner: string;
  context: string;
  documentId: string;
  versionId: string;
  initial: ReaderLocation;
  restore?: { scroll_x?: unknown; scroll_y?: unknown };
  personal: PersonalOperations;
  onLocation(location: ReaderLocation): void;
  onNotice(message: string): void;
  /**
   * Typed page-local region selection carrying the channel's CAPTURED
   * version/page/host-context identity beside the region id; the host binds
   * the stored selection to that exact identity and never retags it.
   */
  onRegion?(selection: { versionId: string; pagePath: string; context: string | null; regionId: string }): void;
}
const uuid = (value: unknown): value is string => typeof value === "string" && /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/.test(value);
const message = (cause: unknown) => cause instanceof Error ? cause.message : String(cause);

// The parent keys this component only for deliberate host navigation. Child
// observations can rerender the surrounding Reader without replacing its frame.
export function IsolatedViewer(props: ViewerProps) {
  const [initial] = useState(props.initial);
  const frame = useRef<HTMLIFrameElement>(null);
  const form = useRef<HTMLFormElement>(null);
  const callbacks = useRef(props);
  callbacks.current = props;
  const [failure, setFailure] = useState<string | null>(null);
  const [loaded, setLoaded] = useState(false);
  const [retry, setRetry] = useState(0);
  const reopening = useRef(false);
  const [closing, setClosing] = useState(false);
  const [waiting, setWaiting] = useState<string | null>(null);
  const [frameName] = useState(() => "haeryong-reader-" + crypto.randomUUID());

  const lease = useMemo(() => {
    const captured = props.controller.captureDispatchAuthority();
    return new RenderSessionLease({
      client: props.controller.client, owner: props.owner, version: props.versionId, context: props.context, storage: localStorage,
      guard: () => props.controller.requireVerifiedDispatch(captured),
      failure: (error) => { setLoaded(false); setFailure(message(error)); },
      notice: (notice) => setWaiting(notice.message),
    });
  }, [props.controller, props.owner, props.versionId, props.context, retry]);

  const reopen = async () => {
    if (reopening.current) return;
    reopening.current = true; setClosing(true);
    try {
      // A known S may be replayed by the same K. Confirm its old close
      // before a new lease can recover/reuse that receipt.
      await lease.stop();
      setRetry((value) => value + 1);
    } catch (error) { setFailure("이전 페이지 연결의 종료를 확인하지 못했습니다: " + message(error)); }
    finally { reopening.current = false; setClosing(false); }
  };

  useEffect(() => {
    const controller = props.controller, client = controller.client;
    const captured = controller.captureDispatchAuthority();
    let active = true, origin: string | null = null, channel: ViewerChannel | null = null;
    let generation = 0, announcement: string | null = null;
    let poll: ReturnType<typeof setInterval> | null = null;
    let positionRequest: string | null = null, applyRequest: string | null = null;
    let current = initial, observed: string | null = null;
    let lastPosition: string | null = null;
    const guard = () => { if (!active) throw new Error("이전 페이지 연결입니다."); controller.requireVerifiedDispatch(captured); };
    const notify = (error: unknown) => { if (active) callbacks.current.onNotice(message(error)); };
    const target = (kind: PersonalTarget["kind"], page: string | null = null): PersonalTarget => ({ version: props.versionId, kind, page });
    const observe = (report: Record<string, unknown>) => {
      if (typeof report.page_path !== "string" || typeof report.page_query !== "string" || typeof report.page_fragment !== "string") return;
      current = { page: report.page_path, query: report.page_query, fragment: report.page_fragment };
      callbacks.current.onLocation(current);
      const next = JSON.stringify(current);
      if (next === observed) return;
      observed = next;
      void props.personal.save(target("reading-position"), { page_path: current.page, page_query: current.query, page_fragment: current.fragment, opened: true }).catch(notify);
    };
    const onMessage = (event: MessageEvent) => {
      const data: unknown = event.data;
      if (!active || event.source !== frame.current?.contentWindow || event.origin !== origin || !data || typeof data !== "object") return;
      const loaded = data as Record<string, unknown>;
      if (loaded.type !== "viewer.loaded" || loaded.document_id !== props.documentId || loaded.version_id !== props.versionId || typeof loaded.page_path !== "string" || !uuid(loaded.render_instance_id) || !uuid(loaded.announcement_id)) return;
      if (announcement === loaded.announcement_id) return;
      announcement = loaded.announcement_id;
      const own = ++generation;
      channel?.dispose(); channel = null;
      if (poll) clearInterval(poll);
      positionRequest = null; applyRequest = null; lastPosition = null;
      setLoaded(false);
      // New documents independently establish their canonical stored page.
      // Author history aliases never select a different source-byte binding.
      void client.renderBootstrap(props.versionId, loaded.page_path).then((boot) => {
        guard(); if (own !== generation || boot.selected_page !== loaded.page_path) return;
        const page = loaded.page_path as string;
        const pageGuard = () => { guard(); if (own !== generation) throw new Error("이전 페이지 연결입니다."); };
        const contextClient = client as unknown as ContextClient;
        const identity = { libraryId: controller.state.capabilities!.library_id, principalId: controller.state.session!.principal_id, sessionId: controller.state.session!.session_id };
        const next = new ViewerChannel({
          frame: frame.current!.contentWindow!, origin: origin!,
          page: { document_id: props.documentId, version_id: props.versionId, page_path: page },
          guard: pageGuard,
          contexts: (binding, boundGuard) => new ContextTransferHost(contextClient, new BrowserContextJournal(localStorage), identity, binding, boundGuard, freshContextVerifier(contextClient)),
          ready(payload) {
            pageGuard(); setLoaded(true); observe({ ...payload, page_path: page });
            if (Number.isFinite(payload.scroll_x) && Number.isFinite(payload.scroll_y)) lastPosition = JSON.stringify([payload.scroll_x, payload.scroll_y, current.query, current.fragment]);
            const region = new URLSearchParams(location.hash.slice(1)).get("region");
            if (region) next.focus(region);
            const publicOrigin = client.publicOrigin;
            if (publicOrigin) next.route(new URL(`/documents/${props.documentId}/versions/${props.versionId}`, publicOrigin).href);
            const beginSnapshots = () => {
              if (poll) clearInterval(poll);
              poll = setInterval(() => {
                try {
                  pageGuard();
                  if (positionRequest) return;
                  positionRequest = crypto.randomUUID();
                  next.requestSnapshot(positionRequest);
                } catch (error) { notify(error); }
              }, 750);
            };
            if (props.restore && page === initial.page) {
              applyRequest = crypto.randomUUID();
              next.applyPosition({ correlation_id: applyRequest, position: props.restore });
              // Polling begins only after the correlated restore acknowledgement.
            } else beginSnapshots();
          },
          positionApplied(correlation, result) {
            if (correlation !== applyRequest || !["fragment", "scroll", "none"].includes(String(result.applied))) return;
            applyRequest = null;
            if (result.applied === "scroll" && props.restore) lastPosition = JSON.stringify([props.restore.scroll_x ?? 0, props.restore.scroll_y ?? 0, current.query, current.fragment]);
            if (poll) clearInterval(poll);
            poll = setInterval(() => {
              try { pageGuard(); if (!positionRequest) { positionRequest = crypto.randomUUID(); next.requestSnapshot(positionRequest); } } catch (error) { notify(error); }
            }, 750);
          },
          position(correlation, position) {
            if (correlation !== positionRequest || !Number.isFinite(position.scroll_x) || !Number.isFinite(position.scroll_y)) return;
            positionRequest = null;
            const stamp = JSON.stringify([position.scroll_x, position.scroll_y, current.query, current.fragment]);
            if (lastPosition === null) { lastPosition = stamp; return; }
            if (stamp === lastPosition) return;
            lastPosition = stamp;
            void props.personal.save(target("page-position", page), { page_query: current.query, page_fragment: current.fragment, position: { scroll_x: position.scroll_x, scroll_y: position.scroll_y } }).catch(notify);
          },
          location: observe,
          display(type, payload) {
            // The captured channel identity (this channel's exact page and
            // the mount's version) travels with the region id; the host
            // matches the tuple before assigning a selection, so late or
            // cross-page events never rely on commit ordering.
            if (type === "region.selected") {
              callbacks.current.onRegion?.({ versionId: props.versionId, pagePath: page, context: props.context, regionId: String(payload.region_id) });
              callbacks.current.onNotice(`선택한 영역: ${String(payload.region_id)}`);
            } else {
              callbacks.current.onNotice("페이지 context를 확인했습니다.");
            }
          },
          error: (code, detail) => callbacks.current.onNotice(`페이지 오류(${code}): ${detail}`),
        });
        channel = next;
        next.accept(event);
      }).catch(notify);
    };
    window.addEventListener("message", onMessage);
    setFailure(null); setLoaded(false); setWaiting(null);
    void lease.start().then((session) => {
      if (!active) return;
      setWaiting(null);
      guard(); origin = new URL(session.render_origin).origin;
      const artifact = "/" + initial.page.split("/").map(encodeURIComponent).join("/") + (initial.query ? "?" + initial.query : "") + (initial.fragment ? "#" + initial.fragment : "");
      const handoff = form.current;
      if (!handoff) throw new Error("페이지 열기 준비를 완료하지 못했습니다.");
      handoff.action = session.handoff.url + "?page=" + encodeURIComponent(artifact);
      (handoff.elements.namedItem("secret") as HTMLInputElement).value = session.handoff.secret;
      handoff.submit();
    }).catch((error) => { if (active) setFailure(message(error)); });
    return () => {
      active = false; generation++;
      window.removeEventListener("message", onMessage);
      if (poll) clearInterval(poll);
      channel?.dispose();
      void lease.stop().catch(() => { /* Expiring delivery remains owned by its captured session. */ });
    };
  }, [props.controller, props.owner, props.context, props.documentId, props.versionId, props.personal, initial, retry, lease]);

  return <section className="reader-content" aria-label="결과 뷰어">
    {failure && <div className="banner" role="alert"><p>페이지 연결을 확인하지 못했습니다: {failure}</p><button disabled={closing} onClick={() => void reopen()}>{closing ? "이전 연결을 닫는 중…" : "다시 열기"}</button></div>}
    {!failure && !loaded && <p role="status">{waiting ?? "페이지를 여는 중…"}</p>}
    {(failure || !loaded || closing) && <button type="button" onClick={() => location.reload()}>화면 전체 새로고침</button>}
    <form ref={form} method="POST" target={frameName} hidden><input type="hidden" name="secret" /></form>
    <iframe key={retry} ref={frame} name={frameName} title="결과 뷰어" className="reader-frame" src="about:blank" sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-top-navigation-by-user-activation" />
  </section>;
}
