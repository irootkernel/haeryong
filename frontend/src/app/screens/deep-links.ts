import type { ConfigurationView } from "../../client/types.ts";
import { safeReturn } from "../routes.ts";
import { referringPage, type ReaderPosition } from "./reader-navigation.ts";

/** Only the authenticated configuration/attachment chooses the transport base. */
export function copiedLink(configuration: ConfigurationView, publicOrigin: string | null | undefined, route: string): { url: string; localOnly: boolean } {
  const network = configuration.configuration.network as { remote_enabled?: boolean; external_base_url?: string } | undefined;
  const remote = network?.remote_enabled === true;
  const base = remote ? network.external_base_url : publicOrigin;
  if (!base) throw Error("승인된 연결 주소를 확인할 수 없습니다.");
  const parsed = new URL(base);
  if (!["http:", "https:"].includes(parsed.protocol) || (remote && parsed.protocol !== "https:") || parsed.username || parsed.password || parsed.search || parsed.hash || parsed.pathname !== "/") throw Error("승인된 연결 주소가 올바르지 않습니다.");
  if (safeReturn(route) !== route) throw Error("복사할 읽기 경로가 올바르지 않습니다.");
  return { url: parsed.origin + route, localOnly: !remote };
}
export function openVersions(documentId: string, versionId: string, owner: string, position: ReaderPosition, sourceURL = location.pathname + location.search + location.hash): void {
  const source = referringPage({ owner, url: sourceURL, context: history.state?.haeryongReaderContext, position }, owner);
  if (!source) throw Error("읽기 복귀 문맥을 확인하지 못했습니다.");
  history.replaceState({ ...history.state, haeryongReaderPosition: source }, "", source.url);
  history.pushState({ haeryongReaderContext: crypto.randomUUID(), haeryongVersionsReturn: { source, referenceReturn: history.state?.haeryongReferenceReturn } }, "", `/documents/${documentId}/versions?${new URLSearchParams({ from_version: versionId })}`);
  window.dispatchEvent(new PopStateEvent("popstate"));
}
export function returnFromVersions(owner: string): boolean {
  const value = history.state?.haeryongVersionsReturn;
  const source = referringPage(value?.source, owner);
  if (!source) return false;
  history.pushState({ haeryongReaderContext: source.context, haeryongReaderPosition: source, haeryongReferenceReturn: referringPage(value.referenceReturn, owner), haeryongVersionsFocus: true }, "", source.url);
  window.dispatchEvent(new PopStateEvent("popstate"));
  return true;
}

/** Restore the host opener after the selected frame/region has acknowledged,
 * so subsequent initial viewer focus cannot steal it again. */
export function restoreVersionsFocus(): void {
  if (!history.state?.haeryongVersionsFocus) return;
  const opener = document.getElementById("reader-versions");
  if (!opener) return;
  opener.focus();
  history.replaceState({ ...history.state, haeryongVersionsFocus: false }, "", location.href);
}
