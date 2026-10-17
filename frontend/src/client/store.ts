// Credential storage for the trusted origin. The browser store keeps the
// remembered bearer in origin-scoped localStorage under the retained key so an
// already paired browser stays paired. Storage failure falls back to an
// honest memory-only mode instead of pretending the token was remembered.
import { SESSION_STORAGE_KEY } from "./types.ts";

export interface CredentialStore {
  read(): string | null;
  write(token: string): boolean;
  clear(): void;
  readonly storageAvailable: boolean;
}

export class BrowserCredentialStore implements CredentialStore {
  #available: boolean;

  constructor() {
    this.#available = false;
    try {
      this.#available = typeof localStorage === "object" && localStorage !== null;
    } catch {
      this.#available = false;
    }
  }

  get storageAvailable(): boolean {
    return this.#available;
  }

  read(): string | null {
    if (!this.#available) return null;
    try {
      return localStorage.getItem(SESSION_STORAGE_KEY);
    } catch {
      return null;
    }
  }

  /**
   * Remove any previously remembered token, then remember the new one. A
   * failed write leaves storage empty so reload honestly re-pairs instead of
   * silently restoring the old credential.
   */
  write(token: string): boolean {
    if (!this.#available) return false;
    try {
      localStorage.removeItem(SESSION_STORAGE_KEY);
    } catch {
      return false;
    }
    try {
      localStorage.setItem(SESSION_STORAGE_KEY, token);
      return true;
    } catch {
      return false;
    }
  }

  clear(): void {
    if (!this.#available) return;
    try {
      localStorage.removeItem(SESSION_STORAGE_KEY);
    } catch {
      // A failed removal cannot manufacture authority; the in-memory token is
      // still dropped by the controller.
    }
  }
}

/** In-memory store for tests and the native entrypoint binding. */
export class MemoryCredentialStore implements CredentialStore {
  #token: string | null = null;
  get storageAvailable(): boolean {
    return false;
  }
  read(): string | null {
    return this.#token;
  }
  write(token: string): boolean {
    this.#token = token;
    return false;
  }
  clear(): void {
    this.#token = null;
  }
}
