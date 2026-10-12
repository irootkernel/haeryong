# Worked multi-page directory example

**Synthetic fixture, not a real project analysis. Documentation baseline 1.5.0.** This example is one result directory with three HTML pages, shared module/worker files, a Unicode filename, an ordinary web manifest and an empty directory. Materialize the payload blocks with UTF-8, LF line endings and the final newline shown. After implementation, `haeryong import ./explanation --project PROJECT_ID` should accept the directory without any Haeryong manifest or hand-assigned ID.

The optional metadata below is a separate explicit input, not a payload filename convention. Omit it to exercise ordinary raw import with source_basis unverified. Supplying it illustrates a synthetic retained source rule and page-local region enrichment. All three pages use overview as a region ID, which is valid because page_path is part of region identity. These fixture definitions are not evidence that a renderer, importer or provider has run.

| Path | Purpose |
| --- | --- |
| index.html | Conventional landing page with links to both other HTML pages |
| pages/retry.html | Module-driven steps with its own overview region, query and fragment |
| pages/결과.html | Independent HTML page with a preserved Unicode filename and another overview region |
| assets/retry.js / assets/count-worker.js | Relative ES module and module-worker loading |
| source.txt | Ordinary retained payload file also used by the optional source-evidence metadata |
| manifest.json | Ordinary web manifest payload, never automatically interpreted as Haeryong metadata |
| exports/ | Represented empty directory preserved by a complete directory export |

## Payload file: `index.html`

```html
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Retry result</title></head>
<body><main data-haeryong-region="overview"><h1>Selective retry</h1>
<p>Illustration: preserve successful roles and retry only failed roles.</p>
<nav><a href="pages/retry.html?scenario=partial#demo">Try the steps</a> <a href="pages/결과.html">Result notes</a></nav>
<p><a href="source.txt">Synthetic source rule</a></p></main></body></html>
```

## Payload file: `pages/retry.html`

```html
<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Retry steps</title></head>
<body><main data-haeryong-region="overview"><h1>Retry steps</h1>
<section id="demo"><p>Only failed roles enter the next illustrative step.</p>
<button id="next" type="button">Next step</button><p id="step">Step 0</p></section>
<a href="../index.html">Overview</a> <a href="결과.html">Result notes</a>
</main><script type="module" src="../assets/retry.js"></script></body></html>
```

## Payload file: `pages/결과.html`

```html
<!doctype html>
<html lang="ko"><head><meta charset="utf-8"><title>결과</title></head>
<body><main data-haeryong-region="overview"><h1>결과</h1>
<p>이 페이지는 실제 실행 추적이 아닌 설명용 예시입니다.</p>
<a href="retry.html?scenario=partial#demo">단계 보기</a> <a href="../index.html">처음으로</a>
</main></body></html>
```

## Payload file: `assets/retry.js`

```javascript
const worker = new Worker(new URL("./count-worker.js", import.meta.url), { type: "module" });
let step = 0;
worker.addEventListener("message", (event) => {
  step = event.data;
  document.getElementById("step").textContent = "Step " + step;
});
document.getElementById("next").addEventListener("click", () => worker.postMessage(step));
```

## Payload file: `assets/count-worker.js`

```javascript
self.addEventListener("message", (event) => self.postMessage((event.data + 1) % 3));
```

## Payload file: `source.txt`

```text
Fixture rule: successful roles remain completed; only failed roles are queued again.
```

## Payload file: `manifest.json`

```json
{
  "name": "Retry example web manifest",
  "start_url": "index.html"
}
```

## Empty directory: `exports`

Create an empty exports directory inside the selected result root. It has an inventory row but no file payload bytes. assets and pages also have one directory row each, inferred from their children. Directory entries are not fabricated as empty files.

## Optional submitted metadata

This JSON is supplied separately using `--metadata PATH` or an HTTP metadata input. It is not needed to import or navigate the HTML. No content.files array or precomputed document/version IDs are required. The service validates actual retained evidence rather than accepting a supplied verified label as proof. Here capture_status concerns the exact synthetic source snapshot. No provider attempt was run, so this example does not claim observed_input; a later source verification and semantic evaluation are separate records.

```json
{
  "schema_version": "1.0",
  "document": {
    "title": "Selective retry fixture",
    "question": "Which roles run again after a partial failure?"
  },
  "version": {
    "source_intent": "historical"
  },
  "content": {
    "default_page": "index.html"
  },
  "regions": [
    {
      "page_path": "index.html",
      "id": "overview",
      "label": "Overview",
      "evidence_ids": [
        "ev-rule"
      ]
    },
    {
      "page_path": "pages/retry.html",
      "id": "overview",
      "label": "Retry steps",
      "evidence_ids": [
        "ev-rule"
      ]
    },
    {
      "page_path": "pages/결과.html",
      "id": "overview",
      "label": "Result notes"
    }
  ],
  "sources": [
    {
      "id": "src-rule",
      "kind": "snapshot",
      "snapshot_sha256": "c88e77d2e80faa53b4916d85cb422e075f6ff9e3cd4371765305c7a1a552f7a6",
      "retention_mode": "selected_snapshot",
      "asset_path": "source.txt"
    }
  ],
  "evidence": [
    {
      "id": "ev-rule",
      "source_id": "src-rule",
      "capture_status": "verified",
      "path": "source.txt",
      "line_start": 1,
      "line_end": 1
    }
  ],
  "generation": {
    "tool": "fixture-author",
    "skill_contract_version": "1.0",
    "model": null
  }
}
```

## Generated service metadata

The following normalized result is illustrative service output when that optional input is used. UUIDs/timestamp are deterministic fixture values, not instructions for an author to invent them. The file inventory is its own stream/cursor resource; it is not embedded into every version response. Without the optional input the source/evidence/region enrichments are absent and source_basis is unverified, while the same files still import and render.

```json
{
  "schema_version": "1.0",
  "document": {
    "title": "Selective retry fixture",
    "question": "Which roles run again after a partial failure?",
    "id": "11111111-1111-4111-8111-111111111111",
    "project_id": "22222222-2222-4222-8222-222222222222"
  },
  "version": {
    "source_intent": "historical",
    "id": "33333333-3333-4333-8333-333333333333",
    "base_version_id": null,
    "created_at": "2026-10-10T00:00:00Z"
  },
  "content": {
    "default_page": "index.html",
    "inventory": "/api/v1/versions/33333333-3333-4333-8333-333333333333/files",
    "payload_sha256": "df1a2aaa44139da7592c4a5c158addfec74b438351c5561ca8e64b4cfe6363da"
  },
  "regions": [
    {
      "page_path": "index.html",
      "id": "overview",
      "label": "Overview",
      "evidence_ids": [
        "ev-rule"
      ]
    },
    {
      "page_path": "pages/retry.html",
      "id": "overview",
      "label": "Retry steps",
      "evidence_ids": [
        "ev-rule"
      ]
    },
    {
      "page_path": "pages/결과.html",
      "id": "overview",
      "label": "Result notes"
    }
  ],
  "sources": [
    {
      "id": "src-rule",
      "kind": "snapshot",
      "snapshot_sha256": "c88e77d2e80faa53b4916d85cb422e075f6ff9e3cd4371765305c7a1a552f7a6",
      "retention_mode": "selected_snapshot",
      "asset_path": "source.txt"
    }
  ],
  "evidence": [
    {
      "id": "ev-rule",
      "source_id": "src-rule",
      "capture_status": "verified",
      "path": "source.txt",
      "line_start": 1,
      "line_end": 1
    }
  ],
  "generation": {
    "tool": "fixture-author",
    "skill_contract_version": "1.0",
    "model": null
  },
  "viewer_profile": "html_js_v1",
  "source_basis": "evidence_recorded"
}
```

## Canonical payload inventory

This exact JSONL is derived from every payload block and directory entry using the [package contract](../contracts/package.md). Its hash includes the ordinary manifest.json file and excludes both metadata blocks above. The file-size element is a decimal string. The inventory is generated by Haeryong during import; the author does not have to write it.

```jsonl
["assets","directory"]
["assets/count-worker.js","file","85","1abd8ebe68ff68f614191a2d3b6c9e0f16e473b35d3fa673675b2b17ea7a7e26"]
["assets/retry.js","file","336","264050b75fda280a7f064697ff6bc346bef90a26f9ecfe589b8013d8c20e491a"]
["exports","directory"]
["index.html","file","438","8075a8e6b4e6d66d6cebcdb91fc42a6fff5a43680281a3ebbf5bff37e238555f"]
["manifest.json","file","72","cae9977467c93517c9d699ed47a6f20464f3fcaeaadeef3a4cffda8b5b85a662"]
["pages","directory"]
["pages/retry.html","file","475","b89466724f16a9fe73830d45635c59435537e385714b000e629cc014144c0590"]
["pages/결과.html","file","355","0f3592d0016883dfe8bf56b2e69f3ce9c21461da8cac6beefb64b8fcea856e67"]
["source.txt","file","85","c88e77d2e80faa53b4916d85cb422e075f6ff9e3cd4371765305c7a1a552f7a6"]
```

## Integrity and verification scope

| Digest | Exact bytes hashed | SHA-256 |
| --- | --- | --- |
| payload_sha256 | Canonical inventory JSONL including one LF per entry | df1a2aaa44139da7592c4a5c158addfec74b438351c5561ca8e64b4cfe6363da |
| supplied_metadata_sha256 | Optional submitted metadata JSON block including its final LF | 1f2cf1cb072cbabb9f3f0a5722d031cefec0f42d27bf86bae6cf30d1f303f7ae |
| manifest_sha256 | Generated service metadata JSON block including its final LF | 7e8b471f187350c2781098e6038aa7d89bc70b3d7d441bd4bc63334b14a95f86 |

All file byte counts/digests, inventory serialization and the two metadata hashes were calculated from the exact blocks while editing these specs. Changing whitespace, Unicode code points, line endings or a final newline changes the relevant hash. This is documentation-fixture verification only; no Haeryong import/render/export or actual browser/module-worker test was executed.

The selected_snapshot source maps exactly to the inventoried source.txt hash. Its historical capture record does not guarantee future availability or prove an AI saw the bytes; captured-read/input-observation/retention rules still apply when asking an AI. source.txt remains ordinary payload bytes even when the metadata is omitted. The explicit source rule is synthetic, so it demonstrates a binding rather than validating real project claims.

## Expected reading and export behavior

For open-without-page, the immutable index.html default applies unless the owner has a version-specific preferred default; all three pages appear in Haeryong's page listing. A copied explicit page link still opens that page, and Continue uses the separate last-opened pointer. Choosing a personal default never changes either metadata hash or payload bytes. The relative retry link preserves scenario=partial and demo. A copied Haeryong link records page=pages%2Fretry.html, page_query=scenario%3Dpartial, page_fragment=demo and, when desired, #region=overview. The Unicode page is independently addressable using the percent-encoded exact page_path; it is not renamed for portability. Equal overview IDs never mix notes, selected state or source assessments across the pages.

Removing index.html from a newly captured variant leaves a valid two-page result and opens the service directory listing unless an explicit default page is supplied. No original page is merged, inlined or rewritten. A normal directory export reproduces the seven files and represented directories; service metadata and personal records are optional separate outputs. A much larger file or many additional files follow the same contract through streaming/on-demand loading, not a different admission policy. Review completion applies to the whole exact directory version and is never inferred from visiting one page.

The worker, Unicode path and multi-page links define intended implementation fixtures; additional cases in [fixtures](../testing/fixtures.md) must verify actual browser/module/worker behavior, large directories, streaming integrity, on-demand navigation, export fidelity and host separation on the declared targets.
