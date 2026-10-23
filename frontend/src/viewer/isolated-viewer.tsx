import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { ShellController } from "../client/controller.ts";
import type { ContextClient } from "../client/context-types.ts";
import type { PersonalTarget, PersonalOperations } from "../app/screens/personal-operations.ts";
import { BrowserContextJournal, ContextTransferHost, freshContextVerifier } from "./context-transfer.ts";
import { featureEnabled } from "../app/screens/library-data.ts";
import type { ReaderPageContext, ReferenceActivation } from "../app/screens/reader-navigation.ts";
import { ViewerChannel } from "./channel.ts";
import { RenderSessionLease } from "./render-session.ts";
import { ReaderLocationTracker, type ReaderLocation } from "./reader-location.ts";

export type { ReaderLocation } from "./reader-location.ts";
export interface ViewerProps {
  controller: ShellController;
  owner: string;
  context: string;
  reload: number;
  initialRegion?: string;
  onChannelReady?(page: ReaderPageContext): void;
  onChannelClosed?(): void;
  onReference?(event: ReferenceActivation): void;
  documentId: string;
  versionId: string;
  initial: ReaderLocation;
  restore?: { scroll_x?: unknown; scroll_y?: unknown };
  personal: PersonalOperations;
  onLocation(location: ReaderLocation): void;
  onNotice(message: string): void;
  /**
   * Host region-outline handle: set while a live page channel exists and
   * cleared with it. Sends one guarded region.focus for real in-frame focus;
   * returns false when the channel/authority is not currently usable.
   */
  focusHandle?: { current: ((regionId: string) => boolean) | null };
  /**
   * Typed page-local region selection carrying the channel's CAPTURED
   * version/page/host-context identity beside the region id; the host binds
   * the stored selection to that exact identity and never retags it.
   */
  onRegion?(selection: ReaderPageContext & { regionId: string }): void;
  /**
   * Honest result of a host-initiated region.focus attempt, observed inside
   * the page, carrying the CAPTURED channel identity beside the values:
   * found reports a matching document element, focused reports it actually
   * receiving focus (a modal overlay or missing element reports false —
   * never a fake success from message delivery alone). The host rejects a
   * late ack whose captured tuple no longer matches the current selection.
   */
  onRegionFocus?(result: ReaderPageContext & { regionId: string; found: boolean; focused: boolean }): void;
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
  const referenceConfig = useRef<(() => void) | null>(null);
  const referencesEnabled = featureEnabled(props.controller.state.capabilities?.features, "references");
  useLayoutEffect(() => { referenceConfig.current?.(); }, [referencesEnabled]);
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
    let referenceGeneration = 0;
    let pendingReference: { correlation: string; id: string; generation: number } | null = null;
    const locationTracker = new ReaderLocationTracker(initial);
    let current = initial, observed: string | null = null;
    let lastPosition: string | null = null;
    const guard = () => { if (!active) throw new Error("이전 페이지 연결입니다."); controller.requireVerifiedDispatch(captured); };
    const notify = (error: unknown) => { if (active) callbacks.current.onNotice(message(error)); };
    const target = (kind: PersonalTarget["kind"], page: string | null = null): PersonalTarget => ({ version: props.versionId, kind, page });
    const observe = (report: Record<string, unknown>) => {
      if (typeof report.page_path !== "string" || typeof report.page_query !== "string" || typeof report.page_fragment !== "string") return;
      current = locationTracker.observe({ page: report.page_path, query: report.page_query, fragment: report.page_fragment });
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
      channel?.dispose(); channel = null; referenceConfig.current = null;
      referenceGeneration++; pendingReference = null;
      if (props.focusHandle) props.focusHandle.current = null;
      callbacks.current.onChannelClosed?.();
      if (poll) clearInterval(poll);
      positionRequest = null; applyRequest = null; lastPosition = null;
      setLoaded(false);
      // New documents independently establish their canonical stored page.
      // Author history aliases never select a different source-byte binding.
      void client.renderBootstrap(props.versionId, loaded.page_path).then((boot) => {
        guard(); if (own !== generation || boot.selected_page !== loaded.page_path) return;
        const page = loaded.page_path as string;
        const pageGuard = () => { guard(); if (own !== generation) throw new Error("이전 페이지 연결입니다."); };
        const capturedPage: ReaderPageContext = { versionId: props.versionId, pagePath: page, context: props.context, reload: props.reload, renderGeneration: loaded.render_instance_id as string };
        const contextClient = client as unknown as ContextClient;
        const identity = { libraryId: controller.state.capabilities!.library_id, principalId: controller.state.session!.principal_id, sessionId: controller.state.session!.session_id };
        let restoreRegion = props.restore && page === initial.page ? props.initialRegion ?? null : null;
        const beginSnapshots = () => {
          if (poll) clearInterval(poll);
          poll = setInterval(() => {
            try { pageGuard(); if (!positionRequest) { positionRequest = crypto.randomUUID(); next.requestSnapshot(positionRequest); } }
            catch (error) { notify(error); }
          }, 750);
        };
        const applyRestore = () => {
          applyRequest = crypto.randomUUID();
          next.applyPosition({ correlation_id: applyRequest, position: props.restore! });
        };
        const next = new ViewerChannel({
          frame: frame.current!.contentWindow!, origin: origin!,
          page: { document_id: props.documentId, version_id: props.versionId, page_path: page },
          guard: pageGuard,
          contexts: (binding, boundGuard) => new ContextTransferHost(contextClient, new BrowserContextJournal(localStorage), identity, binding, boundGuard, freshContextVerifier(contextClient)),
          ready(payload) {
            pageGuard(); setLoaded(true); observe({ ...payload, page_path: page });
            if (Number.isFinite(payload.scroll_x) && Number.isFinite(payload.scroll_y)) lastPosition = JSON.stringify([payload.scroll_x, payload.scroll_y, current.query, current.fragment]);
            referenceConfig.current = () => {
              pageGuard(); referenceGeneration++;
              const enabled = featureEnabled(controller.state.capabilities?.features, "references") && !!callbacks.current.onReference;
              if (!enabled) pendingReference = null;
              next.referenceNavigation(enabled ? (id) => {
                pageGuard();
                if (!featureEnabled(controller.state.capabilities?.features, "references")) return;
                const correlation = crypto.randomUUID();
                pendingReference = { correlation, id, generation: referenceGeneration };
                next.requestSnapshot(correlation);
              } : undefined);
            };
            referenceConfig.current();
            callbacks.current.onChannelReady?.(capturedPage);
            const publicOrigin = client.publicOrigin;
            if (publicOrigin) next.route(new URL(`/documents/${props.documentId}/versions/${props.versionId}`, publicOrigin).href);
            if (props.restore && page === initial.page) {
              // A return entry retains both its selected region and exact scroll.
              // Focus the initial region first, then restore the saved position.
              if (restoreRegion === null) applyRestore();
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
            if (pendingReference?.correlation === correlation && Number.isFinite(position.scroll_x) && Number.isFinite(position.scroll_y)) {
              const pending = pendingReference; pendingReference = null;
              const referenceGuard = () => {
                pageGuard();
                if (pending.generation !== referenceGeneration || !featureEnabled(controller.state.capabilities?.features, "references")) throw new Error("이전 참조 요청입니다.");
              };
              referenceGuard();
              callbacks.current.onReference?.({ ...capturedPage, referenceId: pending.id, position: { scroll_x: position.scroll_x as number, scroll_y: position.scroll_y as number }, guard: referenceGuard });
              return;
            }
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
              callbacks.current.onRegion?.({ ...capturedPage, regionId: String(payload.region_id) });
              callbacks.current.onNotice(`선택한 영역: ${String(payload.region_id)}`);
            } else if (type === "region.focused") {
              if (restoreRegion !== null && payload.region_id === restoreRegion) {
                const expected = restoreRegion; restoreRegion = null;
                if (callbacks.current.initialRegion === expected) applyRestore();
                else beginSnapshots();
              }
              // The ack travels with its OWN captured channel identity (the
              // effect closure's version/page/context), never the latest
              // parent props, so a late A-page ack cannot retag page B.
              callbacks.current.onRegionFocus?.({ ...capturedPage, regionId: String(payload.region_id), found: payload.found === true, focused: payload.focused === true });
            } else {
              callbacks.current.onNotice("페이지 context를 확인했습니다.");
            }
          },
          error: (code, detail) => callbacks.current.onNotice(`페이지 오류(${code}): ${detail}`),
        });
        // T06 focus-C2 (r4): both region entrypoints — the #region hash on
        // ready and the host outline handle — share ONE channel-creation-time
        // captured iframe element. The captured pageGuard runs BEFORE any DOM
        // focus, so a stale handle can never focus a newer frame; the real
        // host frame focus precedes the existing guarded dispatch, and the
        // child's honest region.focused acknowledgment stays the only
        // success authority (WebKit does not elevate frame focus itself).
        const capturedFrame = frame.current;
        const focusRegion = (regionId: string): void => {
          pageGuard();
          if (restoreRegion !== null && regionId !== restoreRegion) { restoreRegion = null; beginSnapshots(); }
          capturedFrame?.focus({ preventScroll: true });
          next.focus(regionId);
        };
        channel = next;
        next.accept(event);
        // Host outline → guarded in-frame region focus, alive exactly as long
        // as this channel generation; cleared with the same effect.
        if (props.focusHandle) props.focusHandle.current = (regionId: string) => {
          try { focusRegion(regionId); return true; } catch { return false; }
        };
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
      active = false; generation++; referenceGeneration++; pendingReference = null;
      referenceConfig.current = null;
      callbacks.current.onChannelClosed?.();
      window.removeEventListener("message", onMessage);
      if (poll) clearInterval(poll);
      channel?.dispose();
      if (props.focusHandle) props.focusHandle.current = null;
      void lease.stop().catch(() => { /* Expiring delivery remains owned by its captured session. */ });
    };
  }, [props.controller, props.owner, props.context, props.documentId, props.versionId, props.reload, props.personal, initial, retry, lease]);

  return <section className="reader-content" aria-label="결과 뷰어">
    {failure && <div className="banner" role="alert"><p>페이지 연결을 확인하지 못했습니다: {failure}</p><button disabled={closing} onClick={() => void reopen()}>{closing ? "이전 연결을 닫는 중…" : "다시 열기"}</button></div>}
    {!failure && !loaded && <p role="status">{waiting ?? "페이지를 여는 중…"}</p>}
    {(failure || !loaded || closing) && <button type="button" onClick={() => location.reload()}>화면 전체 새로고침</button>}
    <form ref={form} method="POST" target={frameName} hidden><input type="hidden" name="secret" /></form>
    <iframe key={retry} ref={frame} name={frameName} title="결과 뷰어" className="reader-frame" src="about:blank" sandbox="allow-scripts allow-forms allow-popups allow-popups-to-escape-sandbox allow-same-origin allow-top-navigation-by-user-activation" />
  </section>;
}
