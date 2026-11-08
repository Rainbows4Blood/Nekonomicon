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

## D&D groups (2026-11-06)

Schema v4 and grouping rules v1 add explicit, resumable **Build D&D groups**
maintenance and atomic live-crawl materialization. See [GROUPS.md](GROUPS.md) for
the catalog investigation, source/kind allowlists, exact rules and limitations.
The production catalog remained read-only throughout investigation and validation.
A bounded test reprocessed 160 real saved responses per storefront in memory:
zero extraction failures, with 21 DTRPG and 23 DM’s Guild grouping warnings.
These are ambiguity/unknown-evidence warnings, not automatically assigned editions.
No authenticated response fixtures or dumps were committed.

Go tests cover edition-parent precedence, publisher/community/compatibility
rules, JSON:API included resources, source/kind collisions and taxonomy drift,
atomic refresh rollback, stale membership exclusion, explicit v3 upgrade with
cancellation, independent task ownership, persisted cursor recovery, and indexed
group/official lookup plans. Frontend checks include grouping task locks and
resume/progress behavior. Headless Chromium exercised the production renderer
with synthetic Wails snapshots at 1440, 980 and 390 px across all maintenance
states, checking overflow and Home/End keyboard navigation. Native Windows
interaction and Linux desktop execution were not repeated for this change.

Windows Wails v2.17.1 production build succeeds at `build/bin/Nekonomicon.exe`,
with the existing `Not found: store.Run` binding-generator warning. The full
Windows Go suite, go vet and frontend checks pass.

Storage-only measurement on Windows/amd64, Go 1.27.1, AMD Ryzen AI 7 350 (16 logical
CPUs), temporary SQLite with WAL/synchronous=NORMAL and all schema-v4 primary keys
and group lookup indexes: `BenchmarkStorage -benchtime=200x -benchmem` measured
2.143 ms/record (18.76 MB/s), 895,252 B/op and 13,562 allocations/op. Dataset: 200
distinct IDs derived from the 40,197-byte supplied fixture. Includes extraction,
group evaluation and transactional raw/derived writes; excludes HTTP and covers.
The fixture mostly exercises nonmatching group outcomes, so this does not measure
full-catalog group backfill or predict its runtime. Query-plan tests separately
verify indexed membership reads; no throughput promise is made.

## Database-wide maintenance (2026-11-07)

The storefront selector and connection indicator now belong only to the Crawler
panel. Metadata extraction, D&D grouping, maintenance counts and issue pages cover
both storefronts regardless of the Crawler selection. Each issue names its store.
The existing per-store checkpoints are claimed atomically and held through the
whole pass; each cursor still commits with its own results. Resuming an older
paused pass retains its cursor and adds the other storefront's pending work.
No schema update or automatic backfill was added.

Regression coverage includes overlapping IDs, failure at the storefront boundary,
restart recovery without retrying already-visited failures, atomic claim rollback,
whole-database End, legacy paused tasks and independent Crawler selection.
Windows Go tests, go vet, frontend checks and the Wails production build pass.
Headless Chromium checked 1440, 980 and 390 px layouts, every maintenance state,
selector visibility and keyboard tab navigation. Native Windows interaction and
Linux execution were not repeated. The build retains the previously documented
Wails binding-generator warning for store.Run.

## D&D browsing, search and fantasy workspace (2026-11-07)

Schema v5 adds an explicit browsing/search read model with separate recoverable
maintenance. Live commits maintain the same projection atomically. No catalog
migration, backfill or live crawl was performed against the user's catalog.
Tests cover all sort directions and keyset boundaries (including missing dates
and overlapping store IDs), edition/official filters, search scopes and escaped
input, stale exclusion, refreshed FTS deletion, rollback of raw/derived/FTS state,
v4 inspection and cancelled upgrade, both-store index recovery, independent task
ownership, safe descriptions, catalog-scoped cover reads and thumbnail bounds.
Query-plan assertions verify sort indexes and FTS usage without raw-response scans.
Frontend tests cover controls, pagination, details/back, safe text, late replies,
three-tab keyboard navigation and restoration of the original theme. Headless
Chromium checks at 1440, 980 and 390 px covered list/details and every index state.
Native Windows interaction and Linux execution have not been repeated.

Measurement: Windows/amd64, Go 1.27.1, AMD Ryzen AI 7 350 (16 logical CPUs), temporary
local SQLite, WAL/synchronous=NORMAL, schema-v5 primary keys, six listing sort
indexes, product lookup index and FTS5 external-content index. Query dataset:
10,000 synthetic D&D products split between both stores, two edition memberships
each, about 2 KiB per response/description; 100 contain the selective search term.
200 measured queries per case, setup excluded. Mean per 50-book page: name 0.423 ms,
next keyset page 0.469 ms, date 0.478 ms, edition+official 0.346 ms, selective FTS
3.013 ms, broad FTS matching all 10,000 books 108.991 ms. The broad case sorts the
matching subset. These measurements exclude HTTP, covers and UI rendering and do
not predict a full production catalog's latency or storage requirements.

The separate realistic-size storage benchmark (200 distinct IDs derived from the
40,197-byte supplied response, metadata/group/catalog projection included) measured
3.612 ms/record, 11.13 MB/s, 918,369 B/op and 13,772 allocations/op. It excludes HTTP
and covers; this fixture does not exercise a large D&D FTS body. Original responses
are preserved and none from authenticated accounts were added as fixtures.

References for the index implementation: [SQLite FTS5](https://www.sqlite.org/fts5.html)
and [SQLite query planning](https://www.sqlite.org/queryplanner.html).

The final Windows Go suite, go vet, frontend checks and Wails production build
pass. The existing store.Run binding-generator warning remains. Schema-upgrade
recovery also preserves a paused task from the prior schema. Description text
uses a single bounded text node, avoiding a DOM node per source line.


## Storefront search, indexing completion, OpenAI connection (2026-11-07)

Schema v6 adds source-aware listing indexes and a covering product freshness
index. Opening stays inspection-only; Apply database update installs indexes over
existing rows. Already indexed books need no re-extraction. Storefront filters are
independent of Crawler selection and participate in cursor validation and FTS.

The reported first-run symptom has a concrete failure path: completed jobs were
persisted before three sequential summary reads shared one 15-second deadline.
A late summary timeout then changed only the UI to blocked/held. Counts now have
separate budgets and cannot override persisted completion. Stale counts have an
explicit Refresh counts recovery path. The covering index avoids response-JSON
pages during counts. The user's original failure was not reproduced on their
catalog; the timeout/completion mismatch is covered by a deterministic regression.

OpenAI uses the documented public-client ChatGPT OAuth flow in native Go. The
sidebar supports saved registrations, browser login/cancellation, plan-permission
status and sign-out. AES-GCM encrypted credentials live outside the catalog; a
separate OS key and cross-process file lock protect storage/rotating refreshes.
OIDC verification uses coreos/go-oidc. No model requests or enrichment run yet.
Future Agents SDK work must obey the ChatGPT-plan Responses API limitations in
SPEC section 16; no SDK runner was added in this connection-only step.

Validation: gofmt, go test ./..., go vet ./..., frontend checks, and a Windows
Wails production build pass. Local HTTP OAuth tests cover PKCE/callback binding,
forged state, issuer/audience/expiry/nonce/signature rejection, identity changes,
issued-client retention after invalid_grant, identity-only grants, cancellation,
refresh, sign-out, uncertain revocation, encrypted storage/tampering/keyring failure
and another app instance holding the credential lock. Frontend tests cover account
controls and new-account selection. Headless Chromium verifies 1440/980/390 px,
D&D filters/details, theme changes, keyboard navigation, count notices, and OpenAI
controls. No live ChatGPT sign-in, inference, catalog migration, or crawl was run.
Native Windows UI and Linux execution were not exercised. The race detector was
not run (this environment has CGO_ENABLED=0). The existing store.Run binding warning
remains; the production executable is build/bin/Nekonomicon.exe.

Additional measurements: same Windows/amd64 Ryzen AI 7 350 environment and
10,000-book synthetic query dataset described above, now schema v6 (six additional
source sort indexes plus products_freshness), WAL/synchronous=NORMAL, 200 iterations,
setup excluded. A 50-book storefront/date page averaged 0.400 ms; storefront +
edition + official/name averaged 0.422 ms. The 200-record, 40,197-byte-response
storage benchmark averaged 2.997 ms/record, 13.41 MB/s. Measurements exclude HTTP,
covers and rendering; they are not a production latency or throughput guarantee.


## Adventure enrichment (2026-11-07)

Schema v7 and catalog projection v2 add adventure eligibility and separate enrichment
results. The existing database-update + browsing-index maintenance path handles old
records; live crawl commits derive the same eligibility. Numeric min_player,
max_player, min_level and max_level columns accompany per-range evidence and model,
source revision, cover hash and outcome. Catalog reindexing preserves enrichment.

The fixed-model Go Responses client sends full original JSON plus a local cover,
uses strict structured output, and exposes web search only in a missing-fields
fallback pass. The first pass is saved before fallback. One cancellable worker
selects missing current fields from both storefronts; repeated passes skip complete
books. No model substitution, automatic inference retry, broad live crawl or live
AI enrichment was used during validation. API/auth/plan compatibility still needs
a real-account enrichment check; local HTTP tests verify the actual request shape.

Validation passed: gofmt, go test ./... -timeout 60s, go vet ./..., eight frontend
tests, and Wails production build at build/bin/Nekonomicon.exe. Tests include source
eligibility, same IDs across stores, complete/missing/partial selection, image/JSON
inputs, conditional web fallback, preserved populated values, cancellation between
passes, incomplete/failed streams, quota stops, numeric persistence, write rollback,
stale-source rejection, restart recovery and reindex preservation. Headless Chromium
at 1440, 980 and 390 px verifies both D&D subtabs, all enrichment states, keyboard
navigation, pagination and safe text/evidence rendering. Native Windows interaction,
Linux execution and the race detector were not run. The prior store.Run Wails binding
warning remains; it does not stop the production build.

Read-only taxonomy verification sampled 200 products per storefront using indexed
joins and returned only taxonomy IDs/labels/contexts. No real product responses or
credentials were copied into the repository. The source fixture independently
confirms the shared Campaigns, Adventures & Modules filter.

Storage measurement: Windows/amd64, Go 1.27.1, AMD Ryzen AI 7 350 (16 logical CPUs),
temporary local SQLite WAL/synchronous=NORMAL, schema v7 including D&D source sort,
freshness and adventure indexes. Two hundred IDs derived from the supplied
40,197-byte response, full live projections, setup excluded: 2.396 ms/record,
16.78 MB/s, 918,841 B/op. This excludes HTTP, covers, AI requests and UI rendering;
it does not predict enrichment cost/latency or production throughput.
## Parallel enrichment and completed unknowns — 7 November 2026

Enrichment now runs up to four books concurrently, with unbuffered dispatch and
checkpoint channels and one coordinator owning database writes. A worker waits for
its storefront checkpoint to commit before web fallback. Stops and global failures
cancel requests and join the workers; restart selects unfinished enrichments from
saved records, so out-of-order completion cannot skip earlier unfinished books.
OAuth token refresh/storage is serialized independently of inference requests.

Schema v8 adds players_done and levels_done separately from numeric coverage.
Successful web fallback completes the requested enrichments even when endpoints
remain null. Unknown values count as enriched, are skipped on subsequent runs for
the same source revision, and appear as Unknown in results and book details.
Transport, cover, validation and storage failures remain operational issues.
The dashboard shows all active books/stages and counts enriched books with unknown
values separately from failures. No concurrency or task configuration is added.

Apply database update explicitly through Data maintenance. Opening only inspects
compatibility. Migration retains existing data and known completions atomically;
legacy unknown results require one follow-up because v7 did not distinguish a
finished fallback from an interrupted storefront checkpoint. A v7 catalog does not
need its browsing index rebuilt for this completion-tracking update.

Validation: gofmt, go test ./... -timeout 60s, go vet ./..., frontend check/test,
and wails build -platform windows/amd64 passed. Added behavior tests cover the
four-request bound, concurrent progress snapshots, out-of-order completion and
resume, cancellation, global failure cancellation, failed fallback retry,
null/partial successful completion, preservation of completed unknown ranges,
atomic schema-upgrade failure/retry, and completed unknowns across database reopen.
Offline headless Chromium checked 1440/980/390 px layouts, four active-book cards,
keyboard navigation and all run states using synthetic bridge responses. No live
inference, broad crawl or migration of the user's catalog was performed. Race
instrumentation was unavailable in this CGO-disabled environment without GCC.

Delivered the primary build/bin/Nekonomicon.exe; no alternative executable name.
The existing Wails binding-generator diagnostic about store.Run remains unchanged;
the production build succeeds.

## Web-search final-answer parsing fix — 7 November 2026

Reproduced the reported "OpenAI returned incomplete structured output" error with
a completed Responses stream containing commentary, a web-search result and valid
final JSON with null endpoints. The client concatenated all assistant output_text
messages; the resulting commentary-plus-JSON string could not be decoded. Prior
unknown-result tests used a fake provider and did not exercise this response shape.

The parser now selects the final_answer message, or the last unphased message for
responses without phase metadata. It combines text parts only within that message,
ignores commentary/tool output, and still rejects refusal, incomplete final
messages, malformed JSON and omitted required fields. Error text distinguishes
those cases without exposing model response contents. Nullable endpoints retain
the existing successful-completion semantics.

Regression coverage exercises the actual local HTTP client through the runner and
SQLite writer, starting with saved errors and unknown storefront checkpoints. The
retry clears errors, counts both books as enriched with unknown values, and sends
no requests for those books on the next run. Additional cases cover populated
ranges, unphased responses, commentary-only output, refused/incomplete answers and
invalid final output after an earlier valid draft.

Validation: gofmt, go test ./... -timeout 60s, go vet ./..., frontend check/test and
the Windows Wails build passed. No live AI calls or catalog mutation were required.
The primary build/bin/Nekonomicon.exe was replaced. No additional schema update is
needed; Enrich retries previously failed records and preserves completed records.

Protocol reference: [OpenAI Responses assistant message phases](https://developers.openai.com/api/docs/guides/reasoning#phase-parameter).

## Retaining streamed answers — 7 November 2026

A bounded live request for the reported H4 book established the actual remaining
failure: response.output_item.done carried a completed final_answer message, while
response.completed reported status=completed with an empty output list. The client
discarded the earlier message. The preceding synthetic tests assumed the final
envelope repeated all output, so they did not catch this account's stream shape.

The client now retains completed message items by output_index, deduplicates them,
reconciles terminal items and selects the final answer in output order. Successful
response.completed remains mandatory: a completed item followed by a failed,
incomplete, interrupted or missing terminal event cannot be committed. Retained
items are capped at 256, alongside the existing 32 MiB stream/8 MiB event bounds.
No schema or UI changes are required. Enrich retries affected pending records.

Live validation used the connected account and exact requested GPT-6.1 Terra model:
H4 The Throne of Bloodstone and FRC1 Ruins of Adventure were copied with their full
saved responses, covers and checkpoints into a temporary catalog. Both ran through
the normal concurrent runner, real HTTP client, parser and SQLite writer. Outcome:
2 enriched, 2 with unknown values, 0 issues and 0 pending; both player and level
completion counts were 2. H4 retained known levels with unknown players; FRC1
completed with both ranges unknown. A second run had no work. The temporary catalog
was removed; the original catalog was opened read-only and was not changed.
Diagnostics printed event types/statuses and counts only, not credentials or raw
response content. No broad live crawl or enrichment run was started.

Regression tests reproduce this empty-terminal-output shape through HTTP and the
runner, including retry of saved failures, durable completion and no repeated work.
They also cover terminal failures after a completed item, incomplete final messages,
commentary-only streams, duplicate and out-of-order item events.

Protocol reference: [Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events).

Validation: gofmt, go test ./... -timeout 60s, go vet ./..., frontend check/test and
the production Windows Wails build passed. The primary build/bin/Nekonomicon.exe
was replaced; no schema update or alternative executable is needed.

## Sixteen concurrent enrichments and fast mode — 7 November 2026

Raised the fixed book pool to 16 while retaining unbuffered dispatch, one writer,
serialized credential refresh, cancellation and source-revision completion markers.
Both storefront and web-fallback requests now set service_tier=priority, the API's
fast-mode setting, while preserving GPT-6.1 Terra and the existing reasoning policy.
The dashboard shows up to 16 active books and describes fast mode as requested.

The concurrency tests now use 32 eligible adventures to verify 16 simultaneous
requests, the upper bound, out-of-order completion/resume and cancellation of the
remaining 15 requests after a global failure. HTTP tests verify the service-tier
setting on both passes. Frontend tests and headless Chromium cover 16 active cards
at 1440/980/390 px, keyboard access and run states.

A bounded live check used synthetic JSON and an image with the connected account.
The literal fast service-tier value returned HTTP 400 on this route. The documented
priority equivalent succeeded twice with valid structured output, but the server
reported service_tier=default. Actual fast processing was therefore not confirmed;
requests opt in, while service availability determines the tier served. No real
catalog records were sent or modified for these checks.

Validation: gofmt, go test ./... -timeout 60s, go vet ./..., frontend checks, layout
checks and the Windows Wails build passed. Replaced build/bin/Nekonomicon.exe. No
schema migration is required.

Reference: [OpenAI Fast mode](https://developers.openai.com/api/docs/guides/fast-mode).

## Rate-limit recovery — 7 November 2026

Enrichment now keeps running after HTTP 429 or structured streaming usage/rate-limit
errors. A shared gate pauses new requests across the 16 workers, backs off from
2 seconds to 5 minutes with jitter, and honors longer Retry-After deadlines. Only
one request probes after the delay, then the remaining books resume. Concurrent
rejections increase the delay once per cooldown; older successes cannot clear a
newer wait. Explicit rate-limit retries continue until recovery or Stop, as requested.
The existing three-minute timeout applies to each attempt, outside the cancellable
wait. Model availability checks use the same recovery behavior.

Retries preserve the current pass and saved storefront checkpoint. Rate-limit
errors do not become saved issues or increment progress/web-pass counts. The
dashboard labels waiting books and the next retry time, and keeps Stop available.
Other authentication, service and invalid-output errors retain their existing
handling. No schema change or real-account request was needed.

Validation: local HTTP tests exercise model-check 429, inference 429, streamed
usage rejection, exact request replay, checkpoint preservation and completed unknown
results. Gate tests cover exponential growth/cap, server deadlines, concurrent
rejections, single-probe release and cancellation. Stop during a one-hour server
wait exits promptly without saving issues, and a new run starts normally.
gofmt, go test ./... -timeout 60s, go vet ./..., frontend checks and headless Chromium
checks at 1440/980/390 px passed. The primary build/bin/Nekonomicon.exe was replaced
using wails build.

References: [OpenAI rate limits](https://developers.openai.com/api/docs/guides/rate-limits),
[ChatGPT-plan error codes](https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery).

## Settings, locations and fictional dates — 8 November 2026

Added three fixed adventure enrichments alongside player and character-level ranges.
Settings hold multiple names and an independent evidence-backed agnostic flag;
named settings and explicit adaptability can coexist. Empty/unknown results do not
imply agnostic. Locations retain optional setting associations. Dates preserve the
source's fictional date/range or relative wording, with optional calendar and
setting associations; the prompt explicitly excludes publication timestamps and
assumed campaign years. Each field carries evidence and source URLs.

Schema v9 adds JSON, known-value and done columns for each new enrichment. The
explicit maintenance update preserves existing player/level completion, including
unknowns, and makes the three new enrichments pending. Opening remains read-only
with respect to schema; no browsing-index rebuild is needed. The existing source
revision checks and live-crawl catalog projection apply to all five enrichments.
Source-first passes finish known facts; fallback finishes remaining fields even
when unknown. Failed/cancelled requests retain checkpoints and completion markers.
The 16-worker pool, fast-mode request and shared rate-limit backoff remain in use.

The dashboard now shows seven summary tiles (five enrichment counts, eligible and
pending books). Dashboard results and book details show settings, locations, dates
and evidence, with completed unknowns distinguished from pending context fields.
Text uses safe DOM rendering, including model-provided names and source strings.

Validation: synthetic local HTTP/stream and SQLite tests cover the expanded output,
required unknown fields, agnostic versus unknown, multiple settings, optional
calendar associations, checkpoint recovery, preservation of finished fields,
completed unknowns and skipped repeat runs. A recreated v8 catalog verifies
explicit upgrade, blocked pre-upgrade crawling, transaction rollback on failure,
cancellation, retry and preservation of numeric and unknown legacy results.
Go tests and vet, frontend checks, and headless Chromium at 1440/980/390 px passed.
Layout checks include context results, book details, run states and keyboard access.
No live enrichment requests were made for validation. Wails build replaced the
primary build/bin/Nekonomicon.exe; users apply schema v9 through Data maintenance.

## Packaged startup regression — 8 November 2026

The setting/location/date UI introduced enrichment-format.js without adding it to
main.go's explicit embedded asset list. Both books.js and enrichment.js imported
the missing file, so the main frontend module never initialized. The static
catalog-opening overlay remained visible and its Abort handler was never attached.
Source-directory browser tests and a successful compilation did not catch this.

Added the missing embedded module and a small independent bootstrap script that
wires safe closing before importing the main app. A module-loading failure now
shows an interface error and hides the spinner. AbortAndClose also accepts closing
after catalog opening finishes, because the frontend may still be loading or have
failed; it always follows the existing shutdown/rollback/lock-release path.

The new dependency test reproduced the missing embedded module before the fix and
passes afterward. An opt-in Chromium test consumes only bytes read from the Go
embedded filesystem and checks ready startup, loading abort, and deliberately
missing-module recovery at 1440 and 390 px. Backend tests verify closing after
opening, lock release and unchanged schema, alongside cancellation/rollback tests.
Go tests/vet, frontend checks and the Windows Wails build passed. The primary
build/bin/Nekonomicon.exe was replaced. No user catalog repair, migration or live
enrichment was performed during this fix.

## Enrichment filters and sorting — 8 November 2026

Schema v10 explicitly builds normalized search terms from saved enrichment, with
atomic cancellation/retry. New results update terms and evidence together. Numeric
filters are inclusive per-endpoint bounds; text filters match any value, using
Unicode case/accent folding. Status selectors keep completed unknowns, unfinished
results and explicit Agnostic settings distinct. Eight new sort fields support
both directions, stable pagination, and missing values last. Dates sort as text.

Regression tests cover combined storefront/edition/FTS/enrichment filters, literal
search terms, zero levels, unknown/pending/Agnostic, all 16 new sort orders over
multiple pages and both stores, stale exclusions, invalid bounds/cursors, index
use, cancelled migration/retry, saved-result backfill and atomic save rollback.
Frontend checks cover control wiring, validation, Reset and Back preservation.
Embedded Chromium tests exercise expanded filters, keyboard navigation, responsive
layout, startup and failure recovery at 1440 and 390 px. Go tests/vet, frontend
checks and the Windows Wails build pass. No live AI calls or user catalog changes.

Storage-only benchmark: Windows amd64, Go 1.26, AMD Ryzen AI 7 350, temporary local
SQLite in WAL/NORMAL mode with production listing/FTS/term indexes. Dataset: 30,000
synthetic roughly 2 KiB product responses across both stores, 24,000 enriched books
with numeric ranges, multiple settings/locations and a fictional date/calendar.
Setup/backfill, HTTP and covers are excluded from query timing. Three iterations
per query measured about 0.66 ms for name browsing and 208–409 ms for enriched
sorts/combined filtering, returning 50 rows. These are small warm-cache samples,
not a throughput guarantee. Broad enriched sorting still orders matching rows;
per-book sort keys and filtering use indexed terms instead of raw JSON scans.

Delivery remains build/bin/Nekonomicon.exe. Existing v9 catalogs require Apply
database update only; saved enrichment is reused without a new AI pass.

## Adventures tab and optional covers — 8 November 2026

Library now has general controls only. Adventures uses the same bounded browse
service with an explicit adventure predicate before pagination and keeps separate
filter/cursor/detail state. Its table presents all enriched fields as columns,
with sortable headings and horizontal keyboard scrolling on narrow windows.
Enrichment remains independent of both browsing tabs. No new schema migration.

The image loader omits missing/unusable covers while retaining path containment
and image-size limits; cancellation and database errors remain errors. Requests
omit input_image entirely when absent and tell the model no image is available.
Full JSON, source-first inference, conditional search, completion markers and
parallel processing are unchanged. A successful retry clears prior cover issues.

Validation covers adventure-only pagination/filters and scope-bound cursors;
separate tab controls, preserved filters, safe column values and details; native
keyboard tabs/headings and embedded UI at 1440/390 px. Local HTTP tests verify
full JSON and omitted image content on both passes. Runner tests cover absent,
missing-file, invalid and oversized covers, clearing old issues, completed unknowns
and no repeat work, plus cancellation/storage failure handling. Go tests, vet,
frontend checks and Windows Wails build passed. No live model calls or user
catalog modifications were used. Delivered build/bin/Nekonomicon.exe.

## Multi-select adventure filters — 8 November 2026

Replaced four free-text fields with native multi-select lists of distinct saved
setting/location/date/calendar values. Reads use the existing term index, current
source/projection checks and original result labels. Selection uses parameterized
exact matching (OR within a field, AND across fields); case/accent variants share
one key. Choice pages are bounded at 100, selections at 64 per field; selections
from other pages remain visible. Cursor fingerprints hash the full query.
No data-model or schema migration is needed beyond v10.

Go regressions cover exact matching, multiple selections, duplicates, literal
special characters, large-selection cursors, choice paging and stale exclusion.
Frontend regressions cover selection payloads, preservation across choice pages
and Back, clearing/reset, failed reads/retry and stale catalog responses. Embedded
Chromium exercises multiple selections and desktop/narrow layouts. Go tests/vet,
frontend checks and the Windows Wails build pass; delivered the primary executable.
