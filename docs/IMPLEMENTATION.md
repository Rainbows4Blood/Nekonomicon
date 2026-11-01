# Implementation and validation

The DTRPG acceptance checks are complete as of 2026-11-01. This file records
their evidence and limits; the standalone HTML mockup remains illustrative.
The production application is the Go/Wails executable with assets in frontend/.

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
largest-ID cursor. Discovery is repeated after pause/restart. A failed commit
retains its batch in memory; later uncommitted results are replayed on resume.
An abrupt process exit can lose that volatile batch. In-memory pauses retain
the product attempt budget; a process restart may replay uncommitted attempts.
End run never removes committed data. Same-URL cover changes and orphan-file
cleanup are deferred.

## Acceptance evidence

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
