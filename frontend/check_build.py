#!/usr/bin/env python3
"""Shared frontend build validation and regeneration.

Preparation (once, explicit, never run by validation):

    cd frontend && npm ci --ignore-scripts

Validation (never installs dependencies; requires the prepared node_modules):

    python3 -B frontend/check_build.py            # verify committed dist
    python3 -B frontend/check_build.py --regenerate   # rebuild committed dist + manifest

Verification builds into a private scratch directory and compares byte-for-byte
with the committed internal/webassets/dist, including assets.manifest.json. The
manifest records a deterministic digest of the frontend inputs; the canonical
Go test in internal/webassets recomputes that digest without Node, so editing
frontend sources without regenerating dist fails `make test`.

Shared input-set rule (keep in sync with internal/webassets/webassets_test.go):
frontend/index.html, package.json, package-lock.json, tsconfig.json,
vite.config.ts, vite.native.config.ts, vite.loader.config.ts and every file under frontend/src/.
"""
import argparse
import hashlib
import json
import os
import re
from pathlib import Path
import shutil
import subprocess
import sys
import tempfile

FRONTEND = Path(__file__).resolve().parent
ROOT = FRONTEND.parent
DIST = ROOT / "internal" / "webassets" / "dist"
MANIFEST_NAME = "assets.manifest.json"
NATIVE_MARKER = "HAEYONG_NATIVE_TRANSPORT_BOUND"
FORBIDDEN_BROWSER_MARKERS = [
    "wails",
    "window.runtime",
    "@wailsapp",
    "NATIVE_TRANSPORT_NOT_IMPLEMENTED",
    NATIVE_MARKER,
    "haeryongNativeCall",
]
FIXED_INPUTS = ["index.html", "native.html", "package.json", "package-lock.json", "tsconfig.json", "vite.config.ts", "vite.native.config.ts", "vite.loader.config.ts"]


def scratch_root():
    volume = Path("/Volumes/RootKernel")
    if os.path.ismount(volume) and os.access(volume, os.W_OK):
        return volume / "tmp"
    return Path(tempfile.gettempdir())


def input_files():
    files = [FRONTEND / name for name in FIXED_INPUTS]
    for path in sorted((FRONTEND / "src").rglob("*")):
        if path.is_file():
            files.append(path)
    return sorted(files)


def inputs_digest():
    digest = hashlib.sha256()
    for path in input_files():
        relative = path.relative_to(FRONTEND).as_posix().encode()
        digest.update(relative)
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def file_digest(root, relative):
    return hashlib.sha256((root / relative).read_bytes()).hexdigest()


def dist_manifest(root):
    files = {}
    for path in sorted(root.rglob("*")):
        if path.is_file() and path.name != MANIFEST_NAME:
            files[path.relative_to(root).as_posix()] = file_digest(root, path.relative_to(root).as_posix())
    return {
        "kind": "haeryong-webassets/v1",
        "inputs_digest": inputs_digest(),
        "input_files": len(input_files()),
        "files": files,
    }


def run(command, timeout):
    result = subprocess.run(command, cwd=FRONTEND, capture_output=True, text=True, timeout=timeout)
    if result.returncode != 0:
        sys.stdout.write(result.stdout[-4000:])
        sys.stderr.write(result.stderr[-4000:])
        raise SystemExit(f"required command failed ({result.returncode}): {' '.join(command[:3])}")
    return result


def main():
    parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    parser.add_argument("--regenerate", action="store_true", help="rebuild internal/webassets/dist and its manifest")
    args = parser.parse_args()

    if not (FRONTEND / "node_modules" / ".bin" / "vite").exists():
        raise SystemExit(
            "frontend dependencies are not prepared; run once explicitly: cd frontend && npm ci --ignore-scripts "
            "(validation never installs dependencies)"
        )

    run([str(FRONTEND / "node_modules" / ".bin" / "tsc"), "--noEmit"], 300)
    run(["node", "--test", "src/client/references.test.ts", "src/app/screens/reader-navigation.test.ts", "src/client/search.test.ts", "src/app/screens/search-state.test.ts", "src/client/controller.test.ts", "src/client/native.test.ts", "src/client/http.test.ts", "src/client/transfer.test.ts", "src/client/download.test.ts", "src/client/native-fs.test.ts", "src/app/reader-context.test.ts", "src/app/router.test.ts", "src/app/screens/library.test.ts", "src/viewer/snapshot.test.ts", "src/viewer/context-transfer.test.ts", "src/viewer/channel.test.ts", "src/viewer/loader.test.ts", "src/app/screens/personal-cas.test.ts", "src/app/screens/personal-operations.test.ts", "src/app/screens/evidence-view.test.ts", "src/app/screens/source-queries.test.ts", "src/app/screens/source-configuration.test.ts", "src/app/screens/source-presentation.test.ts", "src/viewer/render-session.test.ts", "src/client/native-wire.test.ts"], 180)

    keep_scratch = None
    try:
        if args.regenerate:
            run([str(FRONTEND / "node_modules" / ".bin" / "vite"), "build"], 300)
            run([str(FRONTEND / "node_modules" / ".bin" / "vite"), "build", "--config", "vite.loader.config.ts"], 300)
            manifest = dist_manifest(DIST)
            (DIST / MANIFEST_NAME).write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
            print(f"regenerated {DIST} ({len(manifest['files'])} files, inputs {manifest['inputs_digest'][:12]})")
            return 0
        keep_scratch = Path(tempfile.mkdtemp(prefix="haeryong-task01-build-", dir=scratch_root()))
        browser = keep_scratch / "dist"
        native = keep_scratch / "dist-native"
        run([str(FRONTEND / "node_modules" / ".bin" / "vite"), "build", "--outDir", str(browser), "--emptyOutDir"], 300)
        run([str(FRONTEND / "node_modules" / ".bin" / "vite"), "build", "--config", "vite.loader.config.ts", "--outDir", str(browser / "renderer"), "--emptyOutDir"], 300)
        run(
            [
                str(FRONTEND / "node_modules" / ".bin" / "vite"),
                "build",
                "--config",
                "vite.native.config.ts",
                "--outDir",
                str(native),
                "--emptyOutDir",
            ],
            300,
        )

        for path in browser.rglob("*"):
            if path.is_file() and path.suffix in {".js", ".html", ".css"}:
                body = path.read_text(errors="replace")
                for marker in FORBIDDEN_BROWSER_MARKERS:
                    if marker in body:
                        raise SystemExit(f"browser bundle contains native-bridge marker {marker!r}: {path.name}")
        native_js = [path for path in native.rglob("*.js")]
        if not native_js or not any(NATIVE_MARKER in path.read_text(errors="replace") for path in native_js):
            raise SystemExit("native entrypoint build does not contain the native transport marker")
        # Positive entry-document verification: the native bundle must carry
        # its own mount document whose module script resolves to an existing
        # native JS asset carrying the native transport marker. The bundled
        # asset name is hashed, so the source basename is never required.
        native_entry = [path for path in native.rglob("*.html")]
        if not native_entry:
            raise SystemExit("native entry document missing from the native entrypoint build")
        mounted = False
        for path in native_entry:
            for reference in re.findall(r'src="([^"]+\.js)"', path.read_text(errors="replace")):
                target = (native / reference.lstrip("/")).resolve()
                if target.is_file() and NATIVE_MARKER in target.read_text(errors="replace"):
                    mounted = True
        if not mounted:
            raise SystemExit("native entry document does not mount a native JS asset carrying the native transport marker")

        manifest = dist_manifest(browser)
        (browser / MANIFEST_NAME).write_text(json.dumps(manifest, indent=2, sort_keys=True) + "\n")
        committed = dist_manifest(DIST) if DIST.is_dir() else None
        if committed is None:
            raise SystemExit("committed internal/webassets/dist is missing; run with --regenerate first")
        if not (DIST / MANIFEST_NAME).is_file():
            raise SystemExit("committed dist manifest is missing; run with --regenerate")
        committed_recorded = json.loads((DIST / MANIFEST_NAME).read_text())
        if committed_recorded != json.loads(json.dumps(manifest)):
            raise SystemExit("committed dist/manifest drifted from a fresh build of these sources; regenerate dist")
        for name in sorted(manifest["files"]):
            if manifest["files"][name] != committed["files"][name]:
                raise SystemExit(f"dist content drifted for {name}; regenerate dist with matching sources")
        print(f"verified {len(manifest['files'])} committed dist files; inputs digest {manifest['inputs_digest'][:12]}")
        return 0
    finally:
        if keep_scratch is not None and keep_scratch.exists():
            shutil.rmtree(keep_scratch, ignore_errors=True)


if __name__ == "__main__":
    raise SystemExit(main())
