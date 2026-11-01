# Implementation and validation

The acceptance gate in SPEC.md is not yet complete. This file records actual
checks and limitations; the standalone HTML mockup remains illustrative.

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
eligible-domain cookies. The real website/API requests completed, but product
584455 returned a flat object with a top-level productId, unlike the supplied
JSON:API-shaped fixture. Validation correctly rejected the unexpected envelope;
support for that observed protocol difference is pending clarification.

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

## Checks so far

Windows amd64: gofmt, go test ./..., and go vet ./... pass. Tests cover cookie
scope, manual input, Firefox WAL import and expiry, gzip sitemap recursion and
cycles, namespace extensions, product URL mapping, invalid/incomplete XML,
body limits, response classification, redirect/CDN credential isolation, exact
fixture-byte storage (including included), rollback, failed refresh preservation,
out-of-order eligibility, exclusive writers, and abrupt-process batch recovery.

The restricted execution sandbox could not resolve some Go standard-library
packages; the same checks pass outside that sandbox. Go 1.27.1 and a downloaded
Go 1.26.0 toolchain both passed the initial core tests. Linux desktop build and
smoke checks have not run yet.
