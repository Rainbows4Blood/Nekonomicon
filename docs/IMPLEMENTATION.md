# Implementation and validation

## Desktop workspace redesign — 2026-11-06

The frontend now fills the native window, with persistent storefront/navigation
and Local data in the sidebar, a shared status bar, and a flexible main workspace.
Crawler commands, phase progress, committed catalog/cover counts, errors and
connection controls have separate areas. Maintenance reuses the shell with
metadata/progress/issues in the main column and database structure alongside.
Default native size is 1440 × 900. No backend storage or crawl behavior changed.

Windows `npm --prefix frontend run check`, `go test ./...`, `go vet ./...`, and
the production Wails build pass. The validated build is delivered as
`build/bin/Nekonomicon.exe`, replacing the primary executable so existing
symlinks and shortcuts continue to work. Alternative executable names require
an explicit user request, as recorded in `AGENTS.md`.
Wails still emits the previously documented `Not found: store.Run` warning.

Headless Edge exercised the real frontend with synthetic Wails snapshots at
1920, 1440, 1280, 1024, 980, 760, 600, 390 and 360 px. Checks cover crawl and
maintenance states, document/panel overflow, vertical/horizontal keyboard
navigation, command routing, masked cookie fields, loading focus containment,
and light/dark appearance. Desktop and narrow screenshots were inspected;
test counts and paths are illustrative. Renderer regressions cover held phase
highlighting, skipped retry/cover-only phases, cover counts and navigation.
The native WebView2 window and Linux build/rendering were not rerun for this
redesign. No live crawl ran and no user catalog or credentials were changed.

The DTRPG acceptance checks are complete as of 2026-11-01. This file records
their evidence and limits; the standalone HTML mockup remains illustrative.
The production application is the Go/Wails executable with assets in frontend/.

## Migration loading and shutdown — 2026-11-05

Startup and folder changes now open the catalog on a background Go worker.
The app mutex protects short state updates; it is not held while SQLite copies
responses, rolls back, commits, or counts saved products. Snapshot/event delivery
therefore remains responsive. Loading is visible from the initial HTML, and the
4 Hz event pump starts asynchronously once Wails reports DOM readiness.

The blocking overlay shows coarse migration stages and supports keyboard focus,
screen-reader announcements, and reduced motion. Its Abort and close action
bypasses the normal busy-action gate. Window close uses the same lifecycle:
request cancellation, wait for the open worker and explicit transaction rollback,
close the active engine/database, release the writer lock, and ask Wails to quit.
A commit already in progress settles before shutdown, so a successfully committed
upgrade is retained. Rollback/close may take time; the UI stays in Closing until
cleanup finishes. No force-kill or detached backend process is used.

Schema version and migration contents are unchanged by this UI/lifecycle work.
Context-aware operations cover opening pragmas, schema queries, migration SQL and
catalog counts. Tests cancel before copying, during a synthetic product-table
copy, after a copied table, and immediately before commit; they verify v1 schema
and bytes remain intact and the writer lock can be reacquired. App tests verify
responsive snapshots, blocked mutations, both abort/close routes, repeated close
requests, cleanup-before-quit ordering, successful opening, and failed folder
switches preserving the previous catalog. Renderer checks cover the overlay,
error dismissal and duplicate-abort gating. Chromium checks cover 1100/390/360 px,
keyboard trapping, Escape behavior and reduced motion. Native window/process
interaction was not repeated; the Wails quit handoff is covered by an injected
callback that asserts cleanup has completed.

Windows Go tests/vet, frontend checks and Wails build pass; Linux race tests,
vet and Wails build pass with WebKitGTK 4.1. Builds use separate filenames
(`Nekonomicon-loading.exe` / `Nekonomicon-loading-linux`) to avoid replacing the
user’s running executable. The running app and live catalog were not touched.

## DM’s Guild expansion — 2026-11-05

The user authorized the second storefront after the DTRPG acceptance gate.
Both stores use the existing three-phase engine and one catalog writer. There
is no plugin registry or additional storefront placeholder. A native selector
switches stores while idle; active and paused runs keep their original source.
Opening an unfinished catalog selects that run’s source automatically.

DM’s Guild uses `https://api.dmsguild.com/api/vBeta/products/{id}` without the
DTRPG `groupId=1&siteId=10` query. The observed endpoint returns the supported
JSON:API envelope, including `included`. Its website/API cookies, first-party
partition and encrypted session file/key are separate. Switching clears manual
inputs and requires a fresh connection check. Requests, sitemap links and
redirects cannot cross storefronts; cover requests carry no cookies.

Live smoke on Windows imported seven scoped cookies from the existing Firefox
default-release profile. Website/API validation passed in 2.222 s. Robots omitted
sitemap declarations; the fixed `https://www.dmsguild.com/sitemap.xml` fallback
was verified. Three sitemap reads yielded sample products 557, 624 and 662.
Their complete responses (104,987 / 74,484 / 57,652 bytes) survived temporary
SQLite storage byte-for-byte, and all three primary covers validated and saved
atomically. Product HTTP took 1.076 / 0.659 / 0.724 s; commits took 2.137 / 1.569 /
1.573 ms. No full crawl ran. The smoke command removed its temporary catalog
and covers; authenticated response dumps and credentials were not retained.
Unauthenticated website requests encountered a browser challenge; the imported
session passed through the ordinary request path without bypassing it.

Schema v2 adds source to runs and allows both sources in product/failure/cover
keys. Existing v1 runs become DTRPG runs. Migration copies the three constrained
tables inside one transaction, preserving response bytes and cover paths. It
requires extra disk space and may take time for a large catalog; a failed upgrade
rolls back and displays a retry diagnostic. Older binaries reject schema v2.
The migration does not copy or move cover files. Product counts, new/refresh/
retry eligibility, failure clearing and cover scans stay scoped to their source.

New regression coverage includes overlapping product IDs, source-specific
counts/errors/covers, new/refresh/cover passes in the same catalog, wrong-source
resume rejection, unfinished-run startup selection, source-switch gating,
Firefox and redirect isolation, and v1 migration rollback/retry. The production
renderer tests cover all run states and source-dependent hints/cleared fields.
Headless Chromium inspection checked 1100-, 430- and 360-pixel layouts, no
horizontal overflow, and keyboard storefront switching/focus. Native windows
were not re-inspected for this expansion.

Windows Go tests/vet, frontend checks and production Wails build pass. Ubuntu
WSL2 tests with race instrumentation and `webkit2_41`, vet, and the Linux Wails
build pass; Linux used `-s` to embed the same frontend assets checked on Windows.
The existing generated-TypeScript `store.Run` warning remains; the application
uses the JavaScript bridge. Live authentication was checked on Windows only.

Storage-only measurement after this change: Windows amd64, Go 1.27.1, AMD Ryzen
AI 7 350, temporary local C: database, 1,000 distinct rows using the 40,197-byte
supplied fixture, batches of up to 800, schema v2 primary keys and the global
unfinished-run index, WAL / synchronous=NORMAL. `BenchmarkStorage` measured
701,710 ns/record, 57.28 MB/s, 46,196 allocated bytes/record and 117 allocations/
record. This includes transactional writes and source lookup, excludes startup,
v1 migration, HTTP and covers, and is a single offline measurement rather than a
live throughput claim. Earlier measurements below used different batch totals.

## Protocol and authentication

Ported the fixed Firefox 158 User-Agent, Accept/Accept-Encoding headers, scoped
cookies, robots policy, per-origin pacing, shared rate-limit cooldown, and
product-specific 403 classification from the pinned KaijuCrawl reference. That
reference does not add Origin, Referer, or a derived Authorization header.

Firefox import reads cookies.sqlite through a read-only transaction including
WAL, then supplements it with the newest current recovery/session snapshot.
Database cookies win. Container, private-browsing, partition, expiry (including
schema-16 milliseconds), host, path, secure, and creation-order semantics are
preserved. Unsupported contexts are omitted with a value-free diagnostic.
Manual cookies are exact-origin headers. Credentials are AES-GCM encrypted in
the application configuration directory, with the key in the OS credential
manager. No browser protection is bypassed. Changing cookies requires another
connection test. Live responses and imported cookies are never test fixtures.

On 2026-11-01, the selected default-release Firefox profile imported eight
eligible-domain cookies. The website/API check passed. The live API returns a
flat object with top-level productId/image, while the supplied fixture uses a
JSON:API envelope. Both verified shapes are supported, as approved by the user.
The current robots.txt declares no sitemaps. The user approved falling back to
the fixed legacy /sitemap.xml entry point with robots policy still enforced and
a visible diagnostic. A bounded live check read three sitemaps and retrieved
products 1, 3 and 5 (22,047 / 47,984 / 39,426 bytes). Their responses survived
temporary SQLite storage byte-for-byte; HTTP took 735 / 960 / 751 ms and commits
1.32 / 1.02 / 0.50 ms. All three primary covers downloaded, validated, and were
atomically installed before their metadata committed (142 / 92 / 76 ms).
The live CDN requires the `/images/` prefix for relative paths and serves some
valid JPEGs as `image/jpg`; both are covered by regression tests. There were no
product or cover failures in the final sample. This was a small protocol check,
not a full catalog crawl. Temporary authenticated responses and covers are
removed by the smoke command.

## Storage

Windows packaged-app redirection is resolved using an opened catalog handle and
`GetFinalPathNameByHandleW`. A logical `%LOCALAPPDATA%` path can otherwise point
at different files from packaged and ordinary desktop processes. The store uses
the physical parent directory for its lock and covers and exposes that directory
to the UI. No data is moved or merged. Reopening that explicit path retains the
same catalog and unfinished run. The regression test checks file identity,
exclusive locking across the two paths, and saved bytes/run recovery.

SQLite uses WAL, synchronous=NORMAL, foreign keys and a 5-second busy timeout.
All writes pass through the serialized Store owner, using prepared statements
inside short transactions. Product bytes and their run marker commit together;
success clears the corresponding failure in that same transaction. Failed
refreshes retain the previous response. A partial unique index permits only one
unfinished run. An OS file lock prevents two application writers using one data
folder. A process crash releases that lock automatically.

NORMAL preserves application-crash recovery, but an OS crash or power loss can
lose recent transactions. Never back up just the main file while SQLite is live.
There is no export feature in v1.

Initial offline benchmark (2026-11-01): Windows amd64, Go 1.27.1, AMD Ryzen AI 7
350 (8 cores / 16 logical processors), temporary local C: catalog, 4,000 distinct
rows carrying the supplied 40,197-byte fixture, batches of 800, schema/indexes
as in store.go, WAL/NORMAL. Result: 317,651 ns/record (about 3,148 records/s),
126.54 MB/s, 46,487 allocated bytes/record, 123 allocations/record. This includes
prepared writes, existence checks and transaction commits; excludes HTTP,
validation decoding, covers, startup/schema creation, and shutdown. Allocation
counts are cumulative allocation, not peak resident memory. This is one local
measurement, not a network-throughput promise.

The machine has 29,840,748,544 bytes of RAM and a local NTFS C: volume. The
controlled end-to-end benchmark (`go run ./cmd/measure -count 1000`) uses the
same product fixture with synthetic IDs, a local HTTP server, 8 product workers,
4 cover workers, and 14,016-byte generated PNG covers. It saved 1,000 products
and 1,000 covers in 1.218 s: summed product HTTP 0.941 s, validation 0.182 s,
commits 0.363 s, peak sampled Go heap 69,793,232 bytes. These summed timings
overlap because workers run concurrently. This includes discovery, local HTTP,
SQLite and atomic cover files; excludes Internet latency, browser import, UI,
and process memory outside Go's sampled heap. It is not a live crawl benchmark.

## Pipeline and bounds

Cover pagination uses a range seek on the existing `(source, product_id)` primary
key. A 2026-11-01 audit found that omitting the fixed source predicate made SQLite
scan the product index and sort it again for each page on a large catalog. The
query now constrains both source and the product-ID cursor. A regression test
checks page boundaries, joined cover metadata, and an indexed plan without a
temporary sort. This requires no schema migration or catalog rewrite.

Three sequential phases run in a UI-independent coordinator. Workers return
results through unbuffered channels; only the coordinator calls the writer.
Batches flush at 1,000 outcomes, 32 MiB, or 2 seconds from the first result.
Each body is limited independently (16 MiB product, 64 MiB sitemap, 32 MiB
cover), with at most 8 product/4 cover workers by default. Response, gzip and
decoding allocations can temporarily exceed the body size; worker and batch
caps bound those allocations rather than promising a 32 MiB process footprint.
Cover pages stop at 256 records or approximately 32 MiB (plus one record).

Pause cancels scheduling and retry waits while current HTTP requests settle,
then flushes. Resume uses persisted per-product success markers, never a
largest-ID cursor. In-memory resume retains discovered IDs and the unfinished
sitemap frontier; process restart repeats discovery. A failed commit
retains its batch in memory; later uncommitted results are replayed on resume.
An abrupt process exit can lose that volatile batch. In-memory pauses retain
the product attempt budget; a process restart may replay uncommitted attempts.
End run never removes committed data. Same-URL cover changes and orphan-file
cleanup are deferred.

## Acceptance evidence

Discovery regression fixed on 2026-11-01: the live root includes
`/publishers_sitemap.xml`, which matches robots' `Disallow: /publish` prefix.
The earlier bounded product smoke did not reach that late index entry. Such
exclusions now skip only that sitemap, without requesting it or pausing the
run. The UI reports their count. Other download/parsing failures still stop
incomplete discovery. A bounded diagnostic read only robots and the root index;
local HTTP regression tests reproduce the exclusion, continue through remaining
sitemaps into product fetching, and verify pause/resume does not refetch completed
maps. A new run gets a fresh frontier; a restarted process rediscovers as before.

Windows amd64: gofmt, go test ./..., and go vet ./... pass. Tests cover cookie
scope, manual input, Firefox WAL import and expiry, gzip sitemap recursion and
cycles, namespace extensions, product URL mapping, invalid/incomplete XML,
body limits, response classification, redirect/CDN credential isolation, exact
fixture-byte storage (including included), rollback, failed refresh preservation,
out-of-order eligibility, exclusive writers, and abrupt-process batch recovery.
Additional tests cover engine pause/settle/flush, new-product skips, restricted
products continuing to completion, retry-failed recovery, authentication blocks,
retained commit-error batches, timer-triggered commits while other requests are
in flight, bounded attempts across pauses, and preservation of old covers after
partial or HTML responses. Commit failures are injected with a SQLite trigger;
the check exercises rollback/recovery without filling the user's disk.

| Acceptance requirement | Evidence |
| --- | --- |
| Real discovery and authenticated retrieval | Bounded live run: three sitemap-derived products and three primary covers |
| Complete JSON preservation | Byte-for-byte fixture test including top-level `included`; live readback comparison |
| New/refresh/retry behavior | Engine and store tests for skips, upserts, failed-refresh preservation, and retry recovery |
| Crash and out-of-order resume | Separate process terminated inside a transaction; persisted success markers and unfinished-ID eligibility checked after reopening |
| Failure outcomes | Local HTTP tests for restriction, missing products, invalid bodies, auth challenges, redirects, rate limits and incomplete discovery; injected commit failure |
| Independent covers | Separate bounded pool, no product cookies sent to CDN, validated temporary files followed by rename and metadata commit |
| Responsive and accurate UI | 4 Hz snapshots, commit-only counters, timer flush test, production renderer tests for every run state, native layout and keyboard checks |
| Measured storage and pipeline | Reproducible temporary-database commands and measurements above |

## Desktop and platform checks

Windows amd64 (Go 1.27.1, Node.js 22.23.2, Wails 2.17.1): production Wails
build, Go tests/vet, frontend syntax checks and four frontend behavior tests
pass. The actual WebView2 window was checked at desktop and approximately
430-pixel widths, with keyboard focus, sign-in gating, expandable controls and
an empty cover-only run. The real renderer is also exercised for all run states
with synthetic snapshots, including persisted failure text rendered as text.
An opt-in Windows Credential Manager test verifies encrypted session roundtrip,
replacement, absence of plaintext cookies, and tamper rejection using synthetic
credentials. No live credentials are committed or used as fixtures.

Ubuntu under WSL2 (Go 1.26.0, GTK 3.24.52, WebKitGTK 4.1 / 2.52.6):
`go test -race ./internal/...`, full tests and vet with `-tags webkit2_41`, and
the production Wails Linux build pass. The native Linux window rendered and
was inspected through WSLg with isolated temporary configuration/data folders.
WSLg required `LIBGL_ALWAYS_SOFTWARE=1 WEBKIT_DISABLE_COMPOSITING_MODE=1` on this
machine because the GPU device was unavailable. A real Linux Firefox session
and Linux Secret Service persistence have not been exercised; the authenticated
live smoke and native credential persistence test used Windows. Windows race
instrumentation was unavailable without a C compiler; Linux race tests cover
the shared engine, protocol, authentication and storage packages.

The Wails binding generator prints a `Not found: store.Run` warning for the
embedded snapshot's generated TypeScript metadata. The frontend uses the
runtime JavaScript bridge directly, and the native application and renderer
checks pass; generated TypeScript files are not shipped or consumed.

The restricted execution sandbox could not resolve some Go standard-library
packages; the checks above ran successfully outside that sandbox. No full live
crawl was started for UI validation. Offline timing and heap measurements do
not predict Internet throughput or total process resident memory.

## Metadata and maintenance verification — 2026-11-06

Schema v3 adds fixed product metadata mappings and product-scoped child tables.
Desktop opening uses `store.InspectContext`, which never creates a database or
runs schema/transformation work. `store.Open`/`OpenContext` remain explicit
initialization helpers for the CLI measurements and temporary test fixtures;
they are not the desktop opening path. The maintenance UI separately invokes
schema upgrade and extraction. The original JSON and local covers are retained.

The full Go suite and vet pass on Windows, including conservative scalar
conversion, exact decimal/integer precision, sparse integration objects,
JSON:API/flat equivalence, bounded diagnostics, unknown/malformed fields,
raw/derived rollback, stale-data exclusion, old transformation eligibility,
maintenance/crawl exclusion, pause/shutdown, persisted cursor recovery, source
isolation, and inspection without initialization or automatic migration.
Frontend syntax and renderer tests pass, including explicit schema gating,
committed progress, paused ownership and safe issue rendering. Headless Edge
checks of the production frontend passed at 980 px and 390 px for all
maintenance states, horizontal overflow, and keyboard tab navigation. These
checks use synthetic Wails snapshots; they are not a native desktop run.
The Windows Wails production build passes with the previously documented
`Not found: store.Run` binding-generator warning.

A bounded read-only check of the existing catalog projected 256 DTRPG and 187
DM's Guild responses through the new extractor, with zero extraction failures
or warnings in that sample. No raw responses were exported, no authenticated
fixtures were added, no remote requests were sent, and the user's database was
not upgraded or backfilled. Synthetic tests cover the known rare exceptions.

Storage-only measurement: Windows 11 Pro 10.0.26300, AMD Ryzen AI 7 350 (16
logical CPUs), Go 1.27.1, temporary local SQLite catalog, WAL/synchronous=NORMAL,
all schema-v3 primary-key indexes and the existing single-active-run index.
`go test ./internal/store -run '^$' -bench BenchmarkStorage -benchtime=200x -benchmem`
measured 2.113 ms per record (19.02 MB/s), using 200 distinct products derived
from the 40,197-byte supplied fixture with matching product IDs. This includes
JSON projection, all derived inserts and the raw write; it excludes HTTP and
covers. The benchmark now checks that extraction succeeded rather than timing
only the failure path with mismatched synthetic IDs. This small synthetic run
is not a full-catalog performance prediction.

`go run ./cmd/measure -count 200` completed the local-test-server pipeline with
200 saved products and 200 covers in 0.633 s, including 0.477 s in commits and
18.8 MB peak sampled Go heap (not process RSS). It includes discovery, local
HTTP, validation, metadata projection, SQLite, cover download and atomic file
writes; it excludes Internet latency, Firefox import and desktop rendering.
The fixture's repeated taxonomy is less diverse than the real catalog.

WSL/Linux Go 1.27.1 race checks also pass for metadata, store, maintenance and
engine packages. Linux desktop build/native rendering were not rerun for this
change. Maintenance snapshots remain readable during schema writes; schema
version reads use an atomic value rather than waiting for the writer mutex.
