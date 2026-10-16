# Knowledge-value fixture

This disposable Task07 adapter displays two complete generated result directories
through the existing authenticated viewer. It is not the production library,
import service, Ask, Notes or Review. G-VALUE remains `not_run` until the complete
[Task07 closing list](../../docs/tasks/EPIC-001/TASK-001-07.md) is established.

The repository-owned [generation skill](../../skills/use-haeryong/SKILL.md) uses
ordinary multi-page HTML/CSS/JavaScript. The sample directories retain the actual
native-generated nonsensitive file bytes. Their controls predict explicitly
hypothetical source branches; they do not invoke Mulgae or Gaori. Source/citation
integrity and semantic support are assessed separately.

## Operator sidecar

`materialize.py` inventories every regular selected file incrementally and writes
an exclusive operator-owned JSON sidecar outside both source directories. An
initial page need not be `index.html`. There is no extension/count/size/depth
quota or author SDK. The current disposable adapter does not capture selected
symlinks, special entries or empty directories; it makes no production lossless
import/export claim. Implied ordinary directories and all selected file bytes
are preserved. Source bindings and references are optional operator inputs,
not author manifests or admission requirements for ordinary HTML.

```sh
python3 -B spikes/knowledge-value/materialize.py \
  --result-a "$task07_result_a" --result-b "$task07_result_b" \
  --page-a recovery/inventory.html --page-b gaori/outcomes.html \
  --project-a mulgae --project-b gaori \
  --source-bindings "$task07_source_bindings" \
  --references "$task07_references" --output "$task07_sidecar"
go -C spikes/viewer build -o "$task07_binary" ./cmd/fixture
```

Use an explicitly owned unique scratch directory after checking that
`/Volumes/RootKernel` is mounted and writable; otherwise use the system temporary
directory. Variables above identify operator-selected absolute paths. The source
binding input contains capture handles with exact project/revision/path/line
identity, capture path and SHA-256. The optional reference input is a slot/local-ID
map, for example:

```json
{"b":{"prior-compose":{"version":"a","page":"recovery/compose.html","region":"M11"}}}
```

The sidecar pins a reference to the target's Go JSON-map inventory digest. Native
generator bookkeeping may use a different inventory encoding and digest. Keep
both identities and prove the same exact file map; do not rewrite a native pin
or describe one digest as the other.

Only the authorized host starts a fixture, assigns origins, changes private
network mappings or approves a pairing. Pass `--knowledge "$task07_sidecar"`
alongside the viewer's explicit state/base/renderer/remote arguments. Never
assign a previously used renderer origin to another immutable inventory, even
with a fresh state directory. The fixture's persisted slot/origin binding rejects
reassignment within its owned state; the operator also preserves historical
origin nonreuse across states.

## Reader behavior and checks

The optional mode accepts the existing validated current-page ready envelope,
without requiring a scenario object, author SDK or worker declaration. Modules,
assets and authored controls run normally on their separate content origins.
The default G-VIEW corpus and loader remain unchanged.

Ordinary anchors `#source-M-S01` / `#source-G-S01` use the loader's existing
location/state channel. The trusted host resolves only prebound version-local
IDs through the owner-protected fixture endpoint, verifies the captured text's
digest, and displays escaped source text with exact identity. Unknown or
unavailable targets clear previous current evidence and report unavailable;
stale awaited responses cannot restore evidence after binding/location change.
`#reference-prior-compose` similarly opens only its exact prebound inventory,
page and region. This is bounded fixture navigation, not a generic filesystem
proxy, runtime dependency inference or a production reference registry.

Focused Go race tests cover complete regular-file capture, corruption, missing
and extra files, restart reassignment, source identity/privacy/auth, exact prior
reference pins and Python/Go inventory encoding. Actual browser source following,
illustration use, paired same-question comparisons, captured-context follow-up,
held-out questions and synthetic change assessments remain separate evidence.
These offline checks do not establish factual accuracy or reader usefulness.
