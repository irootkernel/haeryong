// Separate native-shell entrypoint (TASK-003-05 binds the real desktop port).
// This build is never embedded in the Go daemon; the browser entrypoint does
// not reference it. The placeholder port answers every named operation with a
// truthful not-implemented error instead of simulating success.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ShellController } from "./client/controller.ts";
import { NativeHaeryongClient, type NativePort } from "./client/native.ts";
import { MemoryCredentialStore } from "./client/store.ts";
import { App } from "./app/shell.tsx";

const NATIVE_TRANSPORT_NOT_IMPLEMENTED = "NATIVE_TRANSPORT_NOT_IMPLEMENTED: desktop transport ships with TASK-003-05";

const port: NativePort = {
  async call() {
    throw new Error(NATIVE_TRANSPORT_NOT_IMPLEMENTED);
  },
};

const client = new NativeHaeryongClient(port);
const controller = new ShellController(client, new MemoryCredentialStore());

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App controller={controller} />
  </StrictMode>,
);

void controller.verify();
