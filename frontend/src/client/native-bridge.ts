// Native-shell bridge acquisition. Imported ONLY by the native entrypoint;
// the browser bundle never references this module. The bridge function is
// injected by the Go harness bootstrap after the native main-frame guard is
// installed, so the entry waits honestly until that handshake arrives.
import { createNativePort } from "./native-wire.ts";
import type { NativePort } from "./native.ts";

/** Truthful identity of this native transport build, asserted by harnesses. */
export const NATIVE_TRANSPORT_ID = "HAEYONG_NATIVE_TRANSPORT_BOUND";

type BridgeCall = (methodID: number, args: unknown[]) => Promise<unknown>;

declare global {
  interface Window {
    haeryongNativeCall?: BridgeCall;
  }
}

export const nativeBridgeReady = (): Promise<BridgeCall> =>
  new Promise((resolve) => {
    const existing = window.haeryongNativeCall;
    if (existing) {
      resolve(existing);
      return;
    }
    window.addEventListener(
      "haeryong-native-bridge",
      () => resolve(window.haeryongNativeCall!),
      { once: true },
    );
  });

export async function createWailsNativePort(): Promise<NativePort> {
  const call = await nativeBridgeReady();
  return createNativePort({
    async invoke(operation, payload) {
      return call(701100, [{ Operation: operation, Payload: payload }]);
    },
  });
}
