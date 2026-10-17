// Browser entrypoint. The remembered bearer stays in the trusted origin's
// localStorage under the retained key; storage events keep tabs consistent.
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ShellController } from "./client/controller.ts";
import { HttpHaeryongClient } from "./client/http.ts";
import { BrowserCredentialStore } from "./client/store.ts";
import { SESSION_STORAGE_KEY } from "./client/types.ts";
import { App } from "./app/shell.tsx";

const store = new BrowserCredentialStore();
const client = new HttpHaeryongClient();
const controller = new ShellController(client, store);

addEventListener("storage", (event) => {
  if (event.key !== SESSION_STORAGE_KEY) return;
  if (!event.newValue) {
    controller.externalRemoval();
  } else if (!controller.hasCredential()) {
    void controller.externalCredential(event.newValue);
  }
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App controller={controller} />
  </StrictMode>,
);

void controller.verify();
