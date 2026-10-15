"""Opt-in local owner wiring for the observed Codex 0.162.1 protocol.

Default CLI only prints an execution-free plan. --execute-smoke is explicit,
owner-only startup/account/config/thread proof with zero turn/start.
No model/effort overrides, auth mutation, API fallback, retry or raw logs.
"""
import argparse
import json
import os
import time
from pathlib import Path

from hardened_harness import (Blocked, DispatchLedger, State, guard_identity,
                              prepare_dispatch)
from stdio_connector import DeadlineClient, LaunchSetupFault, launch_owned

CLI_VERSION = "0.162.1"
BOOTSTRAP_REQUESTS = frozenset(("initialize", "account/read", "config/read", "thread/start"))
NATIVE_ARGV_TAIL = ("app-server", "--listen", "stdio://", "-c", 'model_provider="openai"')


class BootstrapClient(DeadlineClient):
    """No model operation is available through this bootstrap client."""
    def request(self, method, params, timeout=35):
        if getattr(self, "handed_off", False):
            raise Blocked("bootstrap handed off its sole request stream")
        if method not in BOOTSTRAP_REQUESTS:
            raise Blocked("bootstrap allows only initialize/account/config/thread reads")
        if method == "account/read" and (params != {"refreshToken": False}
                or params.get("refreshToken") is not False):
            raise Blocked("bootstrap account reads require explicit refreshToken:false")
        return super().request(method, params, timeout)


class OwnerPreparationFault(Blocked):
    def __init__(self, record, connector=None):
        super().__init__("native owner preparation blocked; inspect safe receipt")
        self.record = record
        self.owned_connector = connector


def selected_argv(argv):
    # This is the exact original demonstrated route, with a caller-selected binary.
    # Restricting the tail prevents accidental model/effort/forced-login/feature flags.
    if (not isinstance(argv, (list, tuple)) or len(argv) != 6
            or not isinstance(argv[0], str) or not argv[0]
            or tuple(argv[1:]) != NATIVE_ARGV_TAIL):
        raise Blocked("explicit argv must match the original stdio OpenAI route")
    return list(argv)


def account_projection(result):
    if not isinstance(result, dict) or not isinstance(result.get("account"), dict):
        raise Blocked("native account response unavailable")
    account = result["account"]
    # Email/account identity/token values are never copied into the projection.
    projection = {"type": account.get("type"), "planType": account.get("planType"),
                  "requiresOpenaiAuth": result.get("requiresOpenaiAuth")}
    if (projection != {"type": "chatgpt", "planType": "pro", "requiresOpenaiAuth": True}
            or projection["requiresOpenaiAuth"] is not True):
        raise Blocked("native ChatGPT Pro account guard blocked dispatch")
    return projection


def config_projection(result):
    if not isinstance(result, dict) or not isinstance(result.get("config"), dict):
        raise Blocked("native effective config unavailable")
    cfg = result["config"]
    projection = {name: cfg.get(name) for name in
                  ("model", "model_provider", "model_reasoning_effort", "forced_login_method")}
    if (projection["model_provider"] != "openai"
            or not isinstance(projection["model"], str) or not projection["model"]
            or projection["forced_login_method"] is not None):
        raise Blocked("native provider/default config guard blocked dispatch")
    effort = projection["model_reasoning_effort"]
    if effort is not None and not isinstance(effort, str):
        raise Blocked("native reasoning effort field malformed")
    return projection


def thread_projection(result, cwd, config):
    if not isinstance(result, dict) or not isinstance(result.get("thread"), dict):
        raise Blocked("native thread response unavailable")
    thread = result["thread"]
    sandbox = result.get("sandbox")
    if (not isinstance(thread.get("id"), str) or not thread["id"]
            or thread.get("cliVersion") != CLI_VERSION
            or thread.get("ephemeral") is not True
            or thread.get("cwd") != cwd or result.get("cwd") != cwd
            or thread.get("modelProvider") != "openai"
            or result.get("modelProvider") != "openai"
            or result.get("approvalPolicy") != "never"
            or not isinstance(sandbox, dict) or sandbox.get("type") != "readOnly"
            or sandbox.get("networkAccess") is not False
            or result.get("model") != config["model"]):
        raise Blocked("native thread identity/permission/default-model guard blocked dispatch")
    effort = config["model_reasoning_effort"]
    if effort is not None and result.get("reasoningEffort") != effort:
        raise Blocked("native thread inherited reasoning effort differs from effective config")
    # Paths, previews, plugin/instruction payloads and thread metadata are not copied.
    return {"id": thread["id"], "cliVersion": thread["cliVersion"], "ephemeral": True,
            "cwd": cwd, "model": result["model"], "modelProvider": "openai",
            "approvalPolicy": "never", "sandbox": {"type": "readOnly", "networkAccess": False},
            "reasoningEffort": result.get("reasoningEffort"),
            "inherited_instruction_count": len(result.get("instructionSources", []))
                if isinstance(result.get("instructionSources", []), list) else None,
            "disabled_plugin_count": len(result.get("disabledPluginIds", []))
                if isinstance(result.get("disabledPluginIds", []), list) else None}


def cleanup_projection(connector, deadline):
    try:
        return connector.close(deadline)
    except Exception as error:
        return {"status": "cleanup_unresolved", "exit_code": None,
                "failure_type": type(error).__name__,
                "scope": "owned connector; handle retained for owner reconciliation"}


class OwnerSession:
    def __init__(self, connector, bootstrap, record, cleanup_deadline):
        self.connector, self.bootstrap, self.record = connector, bootstrap, record
        self.cleanup_deadline = cleanup_deadline

    def prepare_cited_client(self, policy, requested_scopes, captures, question,
                             expected_payload_sha, intent_path, confirm=None):
        """Future owner helper: recheck actual identity and frozen source policy.

        Returns an inherited DeadlineClient but never sends turn/start. The owner
        remains responsible for recording the exact payload/wire and all input
        observations and deciding whether any model dispatch is authorized.
        """
        if self.record.get("bootstrap_handed_off"):
            raise Blocked("one client handoff per native owner session; reconcile existing attempt")
        if self.connector.closed:
            raise Blocked("native owner session closed")
        account = account_projection(self.bootstrap.request("account/read", {"refreshToken": False}))
        config = config_projection(self.bootstrap.request("config/read", {
            "cwd": self.record["cwd"], "includeLayers": False}))
        thread = self.record["thread"]
        guard_identity(account, config, thread)
        if (thread["model"] != config["model"]
                or (config["model_reasoning_effort"] is not None
                    and thread["reasoningEffort"] != config["model_reasoning_effort"])):
            raise Blocked("effective inherited defaults changed; reconcile before dispatch")
        payload = prepare_dispatch(policy, requested_scopes, account, config, thread,
                                   captures, question, expected_payload_sha, confirm)
        state = State(thread["id"], payload, origin="live")
        client = DeadlineClient(state, self.connector, DispatchLedger(intent_path))
        # Request IDs remain unique within this native connection.
        client.next_id = self.bootstrap.next_id
        self.bootstrap.handed_off = True
        self.record["bootstrap_handed_off"] = True
        return client, payload

    def close(self):
        self.record["cleanup"] = cleanup_projection(self.connector, self.cleanup_deadline)
        return self.record["cleanup"]


def open_owner_session(argv, cwd, deadline, cleanup_deadline, *, execute=False, parent_env=None):
    """Explicit future owner call; no process is launched unless execute is True."""
    if execute is not True:
        raise Blocked("execution opt-in required; default is execution-free")
    argv = selected_argv(argv)
    cwd_path = Path(cwd)
    if (not cwd_path.is_absolute() or not cwd_path.is_dir()
            or str(cwd_path.resolve()) != str(cwd_path)
            or not isinstance(deadline, (int, float))
            or not isinstance(cleanup_deadline, (int, float))
            or not time.monotonic() < deadline <= cleanup_deadline):
        raise Blocked("canonical caller cwd and explicit absolute monotonic deadlines required")
    cwd = str(cwd_path)
    record = {"kind": "native owner startup smoke", "phase": "launch", "cwd": cwd,
        "expected_cli_version": CLI_VERSION, "model_effort_overrides": False,
        "removed_child_api_key_env_names": ["OPENAI_API_KEY", "CODEX_API_KEY"],
        "normal_hooks_plugins_preserved": True, "auth_mutation_requested": False,
        "turn_start_count": 0, "provider_outcome": "not_dispatched", "cleanup": None}
    connector = None
    try:
        connector = launch_owned(argv, cwd, dict(os.environ) if parent_env is None else dict(parent_env),
                                 deadline, launch_authorized=True)
        bootstrap = BootstrapClient(State("bootstrap-no-model-turn", "", origin="live"), connector, None)
        record["phase"] = "initialize"
        result = bootstrap.request("initialize", {
            "clientInfo": {"name": "haeryong_preparatory_source", "title": "Haeryong local source proof", "version": "0.0.0"},
            "capabilities": {"experimentalApi": False}})
        if (not isinstance(result, dict) or result.get("platformFamily") != "unix"
                or result.get("platformOs") != "macos"
                or not isinstance(result.get("userAgent"), str)
                or f"/{CLI_VERSION} " not in result["userAgent"]):
            raise Blocked("native initialization version/platform differs from approved baseline")
        record["initialize"] = {"platformFamily": "unix", "platformOs": "macos",
                                "expected_cli_version_observed": True}
        del result
        connector.send({"method": "initialized", "params": {}})
        record["phase"] = "account/read"
        record["account"] = account_projection(bootstrap.request("account/read", {"refreshToken": False}))
        record["account_basis"] = "account/read account.type, planType and requiresOpenaiAuth; no email/token retained"
        record["phase"] = "config/read"
        record["effective_config"] = config_projection(bootstrap.request("config/read", {"cwd": cwd, "includeLayers": False}))
        record["phase"] = "thread/start"
        result = bootstrap.request("thread/start", {"cwd": cwd, "modelProvider": "openai",
            "sandbox": "read-only", "approvalPolicy": "never", "ephemeral": True})
        record["thread"] = thread_projection(result, cwd, record["effective_config"])
        del result
        guard_identity(record["account"], record["effective_config"], record["thread"])
        record["phase"] = "startup_guards_passed_no_turn"
        return OwnerSession(connector, bootstrap, record, cleanup_deadline)
    except LaunchSetupFault as error:
        record["failure_type"] = type(error).__name__
        record["cleanup"] = error.cleanup_receipt
        failure = OwnerPreparationFault(record)
        failure.owned_process = error.owned_process
        raise failure from None
    except Exception as error:
        record["failure_type"] = type(error).__name__
        if connector is not None:
            record["cleanup"] = cleanup_projection(connector, cleanup_deadline)
        raise OwnerPreparationFault(record, connector) from None


def smoke(argv, cwd, deadline, cleanup_deadline, *, execute=False):
    """No turn/start, source disclosure or model output; always close owned session."""
    session = open_owner_session(argv, cwd, deadline, cleanup_deadline, execute=execute)
    try:
        return session.record
    finally:
        session.close()


def main():
    parser = argparse.ArgumentParser(description="Private owner-only startup wiring; default does not execute")
    parser.add_argument("--execute-smoke", action="store_true")
    parser.add_argument("--argv-json")
    parser.add_argument("--cwd")
    parser.add_argument("--deadline-monotonic", type=float)
    parser.add_argument("--cleanup-deadline-monotonic", type=float)
    parser.add_argument("--receipt")
    args = parser.parse_args()
    if not args.execute_smoke:
        print(json.dumps({"execution": "not_run", "turn_start_count": 0,
            "allowlist": sorted(BOOTSTRAP_REQUESTS), "initialized_notification": True,
            "note": "Explicit owner argv/cwd/deadlines/receipt and execute-smoke required; no provider operation now"}))
        return 0
    if any(value is None for value in (args.argv_json, args.cwd, args.deadline_monotonic,
                                      args.cleanup_deadline_monotonic, args.receipt)):
        parser.error("execute-smoke requires argv-json, cwd, both absolute monotonic deadlines and receipt")
    receipt_path = Path(args.receipt)
    # Reserve a caller-selected new private receipt before native startup, never overwrite proof.
    with receipt_path.open("x", encoding="utf-8") as output:
        receipt_path.chmod(0o600)
        try:
            record = smoke(json.loads(args.argv_json), args.cwd, args.deadline_monotonic,
                           args.cleanup_deadline_monotonic, execute=True)
        except OwnerPreparationFault as failure:
            record = failure.record
        except Exception as failure:
            record = {"phase": "blocked_before_launch", "failure_type": type(failure).__name__,
                      "turn_start_count": 0, "provider_outcome": "not_dispatched", "cleanup": None}
        json.dump(record, output, indent=2)
        output.write("\n")
    print(json.dumps({"phase": record["phase"], "turn_start_count": 0,
        "provider_outcome": "not_dispatched", "cleanup_status": (record.get("cleanup") or {}).get("status")}))
    return 0 if (record["phase"] == "startup_guards_passed_no_turn"
                 and (record.get("cleanup") or {}).get("status") == "reaped") else 1


if __name__ == "__main__":
    raise SystemExit(main())
