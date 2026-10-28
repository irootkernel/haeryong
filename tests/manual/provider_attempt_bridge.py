"""Opt-in test transport: one guarded subscription turn, never production dispatch."""
import argparse
import json
import os
from pathlib import Path
import shutil
import sys
import time

ROOT = Path(__file__).resolve().parents[2]
sys.path.insert(0, str(ROOT / "spikes/provider-source"))
import native_owner
from hardened_harness import Capture, ScopePolicy, sha


def emit(value):
    print(json.dumps(value), flush=True)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--scratch", required=True)
    args = parser.parse_args()
    scratch = Path(args.scratch).resolve(strict=True)
    # Version selection is explicit and rechecks every original native guard.
    native_owner.CLI_VERSION = "0.164.0"
    session = None
    record = {"turn_start_count": 0, "kind": "Task01 current ledger transport proof"}
    try:
        session = native_owner.open_owner_session(
            [shutil.which("codex"), *native_owner.NATIVE_ARGV_TAIL], str(ROOT),
            time.monotonic() + 300, time.monotonic() + 330, execute=True)
        record["startup"] = session.record
        text = "provider fixture source"
        question = "This is a transport fixture. Reply with exactly READY; use only the supplied text and no tools."
        scopes = frozenset({("project", "transport-fixture", "selected", "disclose"),
                            ("revision", "transport-fixture", "immutable-fixture"),
                            ("provider", "openai", "chatgpt", "local-owner-only")})
        capture = Capture(("transport-fixture", "immutable-fixture"), text.encode(), sha(text.encode()))
        payload = question + "\n" + text
        client, supplied = session.prepare_cited_client(
            ScopePolicy(scopes), scopes, [capture], question, sha(payload.encode()), scratch / "dispatch-intent.json")
        emit({"phase": "ready", "input_sha256": sha(supplied.encode()), "source_sha256": capture.sha256})
        if sys.stdin.readline().strip() != "dispatch":
            return 2
        record["turn_start_count"] = 1
        result = client.request("turn/start", {"threadId": client.state.thread_id,
                                "input": [{"type": "text", "text": supplied}]}, timeout=90)
        record["accepted"] = client.state.receipt
        emit({"phase": "accepted", "request_id": client.state.turn_id,
              "correlation_id": str(client.state.receipt["request_id"]),
              "session_id": client.state.thread_id, "turn_id": client.state.turn_id})
        # The Go test interrupts/reopens its durable ledger before allowing the
        # transport result back. No second turn or remote cancellation is inferred.
        if sys.stdin.readline().strip() != "finish":
            return 3
        client.wait_terminal(timeout=180)
        answer = client.state.final_answer()
        record["terminal"] = client.state.terminal
        record["input_observation"] = client.state.input_observation()
        record["answer"] = answer
        record["input_sha256"] = sha(supplied.encode())
        record["source_sha256"] = capture.sha256
        emit({"phase": "complete", "answer": answer,
              "observed": record["input_observation"] is not None})
        return 0
    except native_owner.OwnerPreparationFault as error:
        record["startup_failure"] = error.record
        emit({"phase": "blocked", "reason": "native_startup_guard"})
        return 4
    except Exception as error:
        record["failure_type"] = type(error).__name__
        emit({"phase": "unknown", "reason": type(error).__name__})
        return 5
    finally:
        if session is not None:
            record["cleanup"] = session.close()
        path = scratch / "native-receipt.json"
        with path.open("x") as output:
            os.chmod(path, 0o600)
            json.dump(record, output, indent=2)


if __name__ == "__main__":
    raise SystemExit(main())
