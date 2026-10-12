# Directory-result revision: 2026-10-10

**Documentation bundle:** 1.2.0 → 1.3.0

**Authority:** Explicit owner direction, decision D-09. Haeryong serves a personal developer whose AI tools can produce several HTML files and related files in one result directory. The owner asked for directory-level management and rendering with as few input/output constraints as possible, including no artificial HTML-size or file-count limits.

**Status:** The product direction is selected. The contracts below are proposed implementation requirements, not evidence that an importer, renderer or transfer path has been built. All application gates remain not_run.

## Selected product behavior

A logical document is one result directory. Its version is a complete immutable snapshot of the selected tree. The directory can contain any number of HTML pages, nested folders, scripts, styles, images, fonts, media, data files and attachments. A page need not be a self-contained single file, and unrelated HTML files need not be flattened or rewritten into one entry page.

Normal HTML/CSS/JavaScript freedom and remembered owner access from [D-08](2026-10-10-personal-tool-revision.md) remain the baseline. Directory handling must make that workflow practical without another compulsory authoring format.

## Previous constraints removed

| Earlier 1.2.0 proposal | Current requirement |
| --- | --- |
| 10 MiB compressed package, 32 MiB expanded package | No Haeryong total-byte ceiling; stream and resume transfers |
| 256 package entries | No file-count ceiling; enumerate incrementally and paginate indexes |
| 16 MiB render envelope containing the result | Small bootstrap plus on-demand file/range access; no whole-directory envelope |
| Mandatory manifest.json, manual inventory/digests and declared entry HTML | Plain directory/ZIP/single HTML accepted; service generates IDs/inventory and stores optional explicitly supplied metadata separately |
| ASCII-only payload paths and blanket case-fold rejection | Preserve exact relative UTF-8 paths/case; report only actual representation or path-integrity failures without silent renaming |
| Permitted file-type inventory and undeclared-asset rejection | Preserve all selected files; browser-supported preview or exact download, including unknown formats |
| Version-wide region IDs and one reader-page assumption | Page identity (version_id, page_path); optional region identity adds region_id within that page |
| 16 host-state fields, 256-character strings, 32 enum choices and 4 KiB selection state | Ordinary JSON context with automatic streaming/staging and context_ref when needed; control-message framing is not a total context cap |
| Missing source metadata can invalidate arbitrary imported explanations | Import/read success with unknown/unverified source basis; supported verification claims still require actual evidence |

There is no separate directory-depth cap, mandatory SDK, index.html requirement, implicit extension filter or silent exclusion of hidden files. An ordinary web manifest.json remains an original file unless Haeryong metadata is explicitly supplied through the documented metadata input. Optional provenance and region/reference data improve Haeryong integration; they are not prerequisites for preserving and opening the result.

## Page loading and navigation

A valid optional default page is honored. Otherwise use root index.html, then the sole HTML page if there is one, and otherwise show the result's Files/Pages listing. A directory without HTML still remains a browsable/downloadable result. The service-generated listing is application UI and is not inserted into the author's files.

Every page keeps its original relative path. Local HTML links, nested paths, root-relative resources, modules, workers, query strings and fragments must keep normal browser behavior within the selected immutable version. Separate versions or tabs do not share a global current-directory pointer. External URLs retain their ordinary browser behavior.

The canonical trusted reader route uses document/version IDs and page, page_query and page_fragment query parameters, each encoded once for its own raw value. Optional #region is trusted-shell selection. Exact page context survives copied links, login, reload, new tabs and Back/Forward. No hostname or port becomes the logical content identity.

Notes, questions, reading position, citations and optional region references include page_path. Different HTML pages may reuse a region ID. Review still belongs to the complete directory version and changes only through the owner's explicit action; reading one or all pages never completes it automatically.

## Large-content implementation direction

Import/export transfer, hashing and inventory construction operate incrementally. Resumable staging, per-file byte access, cursor-based listings, ordinary HTTP range behavior and on-demand loading avoid buffering a full result in one JSON response or RAM Blob. Finite transfer chunks, buffers, caches and worker concurrency are scheduling/memory mechanisms, not content-admission ceilings. Proxy or control-request constraints must use an automatic smaller-chunk or referenced-object path rather than force the owner to shorten the document.

Preserve atomic publication, exact-byte integrity, receipt replay, object pins and coherent backup/export. The service publishes a complete captured snapshot only after required objects and metadata are durable. Cancelling, losing a connection or filling a disk cannot return a successful partial directory. Source files that change during capture require a stable captured object or a visible retry outcome; do not silently mix different reads under one hash.

The ordinary browser and host still have finite memory, disk, network and format capabilities. A stored file can exceed a particular device's rendering capacity. Report the actual failed operation and preserve the result, offering resume/retry, download or another supported viewer/device as appropriate. This is not permission to reintroduce a universal product cap. A browser reader does not automatically become a Node/Python/native executable host; preserving a file and running its execution environment are different capabilities.

Owner-configured provider/maintenance budgets remain operating policy. A provider's actual context, request or usage limit is shown with processing coverage and recovery. It must not turn into a size/format limit on stored AI results, a silent excerpt truncation, a false full-input answer or unapproved extra paid work.

## Faithful output and source truth

Export the selected original tree as a directory or streamed archive. Preserve file bytes, relative paths and represented directory/link records. Haeryong metadata and personal/relationship records are separate optional outputs; do not require rewriting HTML or injecting a service manifest into the original tree. An actual destination collision or inability to represent a name/link gets a concrete error or explicit conflict resolution. A stored link does not silently copy material outside the selected tree.

Raw import is not a factual-admission test. Missing provenance leaves source basis unknown/unverified. Source-grounded generation, Q&A and maintenance still distinguish actual captured project source from supplied explanation context, and source verification still requires exact target/read/digest evidence. Unresolved optional references remain visible as unresolved without discarding the result. These distinctions prevent false claims while keeping import and reading friction low.

## Traceability and retained decisions

The [requirements](../requirements.md) retain R01–R40, with R02 named Atomic directory import and R37 Configurable work budgets. The 29 inherited acceptance texts remain verbatim in the [catalog](../testing/acceptance-catalog.md). Apply this amendment wherever historical wording assumes a small mandatory package, version-global region, fixed serialized-state maximum or missing-evidence import rejection. Keep its original requirement IDs for traceability rather than pretending the prior source never existed.

[Supplemental cases](../testing/contract-cases.md) add X18 for directory round-trip and inputs above former caps, X19 for multi-page navigation/anchors, and X20 for large context. Existing import, Reader, personal records, generation, remote and release tasks implement and regress these cases. No new Epic or artificial fixed task count is needed; 10 Epics and 61 Tasks remain planned.

Go/Wails, one independent daemon and library, normal JavaScript, remembered owner sessions, initial private remote access, one supported subscription-capable provider, optional source integration, immutable source/citation identity, exact personal review and coherent lifecycle/recovery remain established. Original source and external review files are preserved. Runtime schema_version 1.0 and /api/v1 remain unfrozen; documentation 1.3.0 does not claim compatibility with a shipped runtime.

## Validation and remaining implementation work

The [documentation integrity report](../testing/bundle-validation.md) records actual file/link/metadata/example/hash checks. It does not claim large-file imports, browser compatibility, real provider behavior or performance have been tested. Implementers choose concrete chunk sizes, cache/index construction and renderer routing in the existing gates while preserving D-09.

No additional owner decision blocks this revision. File size, file count, whether an AI may create multiple HTML files, and whether a manifest is mandatory are resolved product requirements, not questions to send back to the owner during implementation.
