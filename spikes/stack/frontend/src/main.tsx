import { useEffect, useState } from "react";
import { createRoot } from "react-dom/client";

type Health = { fixture: string; instance: string };

async function readHealth(): Promise<Health> {
  const signal = AbortSignal.timeout(5000);
  try {
    const response = await fetch("/health", { signal });
    if (!response.ok) throw new Error(`Health HTTP ${response.status}`);
    return await response.json();
  } catch (error) {
    if (signal.aborted) {
      throw new Error("Health request timed out; try again");
    }
    throw error;
  }
}

function App() {
  const client = new URL(window.location.href).searchParams.get("client");
  const [state, setState] = useState("Not checked");
  const [pending, setPending] = useState(false);
  const [attachment, setAttachment] = useState("Not attached");
  const [attaching, setAttaching] = useState(false);
  async function attach() {
    if (!client) return;
    setAttaching(true);
    setAttachment("Connecting to fixture");
    try {
      const identity = await readHealth();
      const response = await fetch(`/attach/${encodeURIComponent(client)}`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ instance: identity.instance }),
        signal: AbortSignal.timeout(5000),
      });
      if (!response.ok) throw new Error(response.status === 400
        ? "Attach HTTP 400: fixture instance mismatch"
        : `Attach HTTP ${response.status}`);
      setAttachment(`Attached to ${identity.instance}`);
    } catch (error) {
      setAttachment(error instanceof DOMException && error.name === "TimeoutError"
        ? "Attachment request timed out; use Retry attachment"
        : `${error instanceof Error ? error.message : "Attachment failed"}; use Retry attachment`);
    } finally {
      setAttaching(false);
    }
  }
  useEffect(() => { void attach(); }, [client]);
  async function check() {
    setPending(true);
    setState("Checking fixture service");
    try {
      const data = await readHealth();
      setState(`${data.fixture}: ${data.instance}`);
    } catch (error) {
      setState(error instanceof Error ? error.message : "Connection failed");
    } finally {
      setPending(false);
    }
  }
  return <main>
    <h1>Stack spike</h1><p>Disposable fixture only.</p>
    <button disabled={pending || attaching} onClick={check}>Check fixture service</button>
    <output aria-label="Health status" aria-live="polite">{state}</output>
    {client && <section aria-label="Client attachment">
      <button disabled={pending || attaching} onClick={attach}>Retry attachment</button>
      <output aria-label="Attachment status" aria-live="polite">{attachment}</output>
    </section>}
  </main>;
}

createRoot(document.getElementById("root")!).render(<App />);
