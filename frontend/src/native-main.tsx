// Separate native-shell entrypoint for the reused pinned beta28 harness
// ReaderMode (TASK-003-03 M2; TASK-003-05 binds the production desktop port).
// This build is never embedded in the Go daemon; the browser entrypoint does
// not reference it. The bridge is injected by Go only after the native
// main-frame guard is installed, so the entry waits for that handshake
// honestly instead of simulating a session.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ShellController } from "./client/controller.ts";
import { NativeHaeryongClient } from "./client/native.ts";
import { MemoryCredentialStore } from "./client/store.ts";
import { App } from "./app/shell.tsx";
import { NATIVE_TRANSPORT_ID, createWailsNativePort } from "./client/native-bridge.ts";

// Harnesses assert this truthful transport identity in the compiled bundle.
Object.defineProperty(globalThis, "__haeryongNativeTransport", { value: NATIVE_TRANSPORT_ID });

async function main(): Promise<void> {
  const port = await createWailsNativePort();
  const client = new NativeHaeryongClient(port);
  const controller = new ShellController(client, new MemoryCredentialStore());
  createRoot(document.getElementById("root")!).render(
    <StrictMode>
      <App controller={controller} />
    </StrictMode>,
  );
  // Native session authority lives in Go: attach through the real facade and
  // verify the production session/capabilities routes. No JavaScript
  // credential exists in any state.
  await controller.attachNativeSession();
}

void main();
