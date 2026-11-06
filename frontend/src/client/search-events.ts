import type { EventStreamResync } from "./types.ts";

// Content-free hints only: every authorized SSE domain frame requires an
// authenticated GET under current authority. Heartbeats/comments carry no
// event field and are ignored; incomplete frames never advance the cursor or
// notify before their terminating blank line.
export function searchInvalidated(type: string): boolean {
  return type.trim().length > 0;
}

export function searchEventLines(
  notify: () => unknown,
  options?: {
    onCursor?: (cursor: string) => void;
    onResync?: (resync: EventStreamResync) => unknown;
  },
): (text: string) => Promise<void> | void {
  let pending = "";
  let frameId: string | null = null;
  let frameEvent = "";
  let frameData: string[] = [];
  let chain: Promise<void> | null = null;

  const dispatchFrame = (id: string | null, type: string, dataText: string): Promise<void> | void => {
    if (type === "resync.required") {
      const fallbackEpoch = id && id.includes(":") ? id.split(":")[0]! : "";
      const fallbackHead = id && id.includes(":") ? id.split(":")[1]! : "";
      let resync: EventStreamResync = {
        reason: "resync.required",
        stream_epoch: fallbackEpoch,
        head: fallbackHead,
        resume_cursor: id ?? "",
      };
      if (dataText.trim().length > 0) {
        try {
          const parsed = JSON.parse(dataText) as Partial<EventStreamResync>;
          resync = {
            reason: typeof parsed.reason === "string" && parsed.reason.length > 0 ? parsed.reason : "resync.required",
            stream_epoch: typeof parsed.stream_epoch === "string" ? parsed.stream_epoch : fallbackEpoch,
            head: typeof parsed.head === "string" ? parsed.head : fallbackHead,
            resume_cursor: typeof parsed.resume_cursor === "string" && parsed.resume_cursor.length > 0 ? parsed.resume_cursor : (id ?? ""),
          };
        } catch {
          // Retain fallback cursor metadata from id: when data is not valid JSON.
        }
      }
      const committedCursor = resync.resume_cursor || id || "";
      const out = options?.onResync ? options.onResync(resync) : notify();
      if (out && typeof (out as Promise<void>).then === "function") {
        return (out as Promise<void>).then(() => {
          if (committedCursor) options?.onCursor?.(committedCursor);
        });
      }
      if (committedCursor) {
        options?.onCursor?.(committedCursor);
      }
      return;
    }
    const out = notify();
    if (out && typeof (out as Promise<void>).then === "function") {
      return (out as Promise<void>).then(() => {
        if (id) options?.onCursor?.(id);
      });
    }
    if (id) {
      options?.onCursor?.(id);
    }
    return;
  };

  return (text: string) => {
    const lines = (pending + text).split(/\r?\n/);
    pending = lines.pop()!;
    for (const line of lines) {
      if (line === "") {
        const id = frameId;
        const type = frameEvent;
        const dataText = frameData.join("\n");
        frameId = null;
        frameEvent = "";
        frameData = [];
        if (!searchInvalidated(type)) {
          continue;
        }
        if (chain !== null) {
          const prev = chain;
          const tracked: Promise<void> = prev
            .then(() => dispatchFrame(id, type, dataText))
            .finally(() => {
              if (chain === tracked) chain = null;
            });
          chain = tracked;
        } else {
          const out = dispatchFrame(id, type, dataText);
          if (out && typeof (out as Promise<void>).then === "function") {
            const tracked: Promise<void> = Promise.resolve(out).finally(() => {
              if (chain === tracked) chain = null;
            });
            chain = tracked;
          }
        }
        continue;
      }
      if (line.startsWith(":")) {
        continue;
      }
      const colon = line.indexOf(":");
      const field = colon === -1 ? line : line.slice(0, colon);
      const rawValue = colon === -1 ? "" : line.slice(colon + 1);
      const value = rawValue.startsWith(" ") ? rawValue.slice(1) : rawValue;
      if (field === "id") {
        frameId = value.trim();
      } else if (field === "event") {
        frameEvent = value.trim();
      } else if (field === "data") {
        frameData.push(value);
      }
    }
    if (chain !== null) {
      return chain;
    }
  };
}
