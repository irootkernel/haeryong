// Shared stream freshness guard. Exactly one reader is acquired per wrapped
// stream; every chunk is checked against the captured authority era before it
// reaches the consumer, cancellation propagates through the reader (never
// through a locked source), and a stale era errors the stream instead of
// feeding a replaced authority's bytes onward.
import { StaleResponseError } from "./types.ts";

export function eraGuardedStream(source: ReadableStream<Uint8Array>, stale: () => boolean): ReadableStream<Uint8Array> {
  const reader = source.getReader();
  return new ReadableStream<Uint8Array>({
    async pull(controller) {
      try {
        const chunk = await reader.read();
        if (stale()) {
          // Abort the underlying read and error the wrapper: a replaced
          // authority's bytes must never complete a consumer's stream.
          await reader.cancel().catch(() => undefined);
          controller.error(new StaleResponseError());
          return;
        }
        if (chunk.done) {
          reader.releaseLock();
          controller.close();
          return;
        }
        controller.enqueue(chunk.value);
      } catch (cause) {
        controller.error(cause);
      }
    },
    cancel(reason) {
      reader.cancel(reason).catch(() => undefined);
    },
  });
}
