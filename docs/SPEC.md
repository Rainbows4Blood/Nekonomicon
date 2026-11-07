# Nekonomicon — DriveThruRPG and DM’s Guild

Status: implementation specification. This replaces the previous design entirely.

## 1. Goal and scope

Build a slick, simple, fast local desktop crawler in Go/Wails. Collect DriveThruRPG and DM’s Guild product API responses and primary covers. The application runs on the user's Windows or Linux machine without a database server or object-storage service.

The DTRPG gate passed on 2026-11-01; the user authorized DM’s Guild on 2026-11-05. Provide a selector for these two implemented storefronts only. Storytellers Vault and Ulisses eBooks remain deferred. No browsing/search, general classification or enrichment system (fixed D&D groups are specified in section 14), cross-store deduplication, network storage, scheduler, or distributed architecture.

## 2. Three sequential phases

1. Discover: read robots declarations, recursively resolve sitemap indexes, collect unique product IDs.
2. Fetch: concurrently request product API responses; one writer commits bounded batches to SQLite.
3. Covers: after product fetching finishes, download primary covers from saved responses to local files.

Complete discovery before product fetching. Maintain IDs in memory, not response bodies for the entire crawl. Each phase is independently cancellable. Covers are optional and can also be run alone.

## 3. Verified DTRPG protocol

Reference implementation: Rainbows4Blood/kaijucrawl at commit f2ced97ac62f24e32abd51fd10b88fb7bda344af.

- Website: https://www.drivethrurpg.com/
- API origin: https://api.drivethrurpg.com/
- Product request: https://api.drivethrurpg.com/api/vBeta/products/{id}?groupId=1&siteId=10
- Canonical product URL: https://www.drivethrurpg.com/en/product/{id}
- Primary image base: https://d1vzi28wh99zvq.cloudfront.net/images/ (live-verified correction on 2026-11-01; the origin-only legacy base returns 403 for the supplied relative path).

Reference files:

- [Adapter and response classification](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs)
- [Sitemap parser](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/sitemap.rs)
- [HTTP compatibility and robots](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs)
- [Authentication](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth.rs)
- [Firefox session import](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth/firefox_session.rs)

These are behavioral references, not a requirement to copy the old queue/storage/parser architecture. Confirm the protocol with a small live authenticated smoke check during implementation; these endpoint details are observed code, not an external API guarantee.

### DM’s Guild protocol (verified 2026-11-05)

- Website: https://www.dmsguild.com/
- API origin: https://api.dmsguild.com/
- Product request: https://api.dmsguild.com/api/vBeta/products/{id} (no DTRPG groupId/siteId parameters).
- Canonical product URL: https://www.dmsguild.com/en/product/{id}
- Fixed fallback: https://www.dmsguild.com/sitemap.xml, verified with the scoped Firefox session.
- The same approved CloudFront `/images/` base serves primary covers.

The API returned the supported JSON:API envelope including `included`; full bytes
are preserved. The compatibility headers, retry/classification rules and three
phases are shared. Each source approves only its own website/API hosts and
Firefox first-party partition. Never reuse one storefront’s cookies for the other.

## 4. Discovery

Load robots.txt for website and API origins, preserving existing policy and header behavior. Extract declared Sitemap URLs. If there are no approved declarations, use the fixed, verified `/sitemap.xml` entry point on the selected storefront website and visibly report this fallback. This exception was approved on 2026-11-01 after the live robots response omitted sitemap declarations and the legacy root yielded matching products. Robots rules still apply; do not guess other roots. Follow sitemapindex entries recursively; parse urlset product entries. Support plain XML, namespaces, escaped URLs, and gzip. Ignore extension entries such as image:loc. Validate XML completeness and enforce decoded-size limits.

Maintain visited sitemap URLs and a deduplicated set of positive product IDs. Accept modern /product/{id}, localized /en/product/{id}, and legacy /product_info.php?products_id={id} forms supported by the old adapter. Language/slug variants resolve to one ID. Reject unrelated hosts and non-product URLs. Preserve optional lastmod as a hint only; v1 does not depend on it to skip updates.

Robots-excluded sitemap URLs are skipped without sending a request, with a visible skipped count; they do not pause the crawl. This behavior was clarified on 2026-11-01. Download or parsing failure after bounded retries still makes discovery incomplete: show the failure and offer retry. Zero IDs from the permitted sitemaps is a visible diagnostic, not an automatic successful empty crawl. Never delete saved products because a sitemap omits them. Do not add listing pagination or numeric-ID guessing.

A fresh process may rediscover sitemaps rather than persist an exact frontier. Within the same process, retain discovered IDs and the unfinished sitemap frontier for Resume, refreshing robots policy first. The discovered total is a snapshot and does not prove that all existing/private products are listed.

## 5. Modes and recovery

- **New products** (default): rediscover, then fetch IDs without a successfully saved product response. Existing failures remain eligible.
- **Refresh all**: rediscover, then fetch all IDs, updating successful responses in place. Retain the last good response if a refresh fails.
- **Retry failed**: retry recorded product failures; no full sitemap pass needed. Cover failures belong to the cover pass.
- **Download covers**: scan saved products for missing or changed primary covers; no product requests needed.

Create a run ID and persist storefront, mode, start time, state, and covers preference. Record last_success_run_id on each product in the same transaction as its JSON. Resume an interrupted refresh using the same run ID and omit already-committed successes. A new run gets a new ID. Failures do not set this success marker.

Only one active run across both storefronts. Storefront selection is locked until the run is ended or completed. Opening a catalog with an unfinished run selects its storefront; connection readiness is never transferred between sources. If an unfinished run exists, show Resume and End run; do not silently start another. Ending a run keeps all committed data; a future refresh starts a new run. Resuming after a process restart rediscovers IDs where needed, so the source set can differ from the initial snapshot. Do not promise an immutable frontier or exact recovery of volatile progress counters.

Pause stops scheduling, lets bounded in-flight requests settle, flushes successful buffers, and then reports Paused. Resume continues that run. Stop/end flushes and closes the run when possible. Abrupt crash may lose the current buffer and queue state; replay is acceptable. Out-of-order worker completion must not skip unfinished IDs: never resume using the largest completed ID or a single frontier cursor.

## 6. Minimal SQLite model

One local SQLite database. Use ordinary JSON TEXT containing the entire API response, including top-level data, relationships, and included. Derived metadata now lives in separate tables as authorized on 2026-11-06; see section 13. Do not transform and reserialize the response merely to store it.

Suggested tables (implementation may refine types without expanding scope):

| Table | Fields and constraints |
| --- | --- |
| products | source TEXT, product_id INTEGER, fetched_at UTC timestamp, last_success_run_id, response_json TEXT NOT NULL; primary key (source, product_id) |
| runs | id primary key, source, mode, phase, state, started_at, finished_at nullable, download_covers |
| failures | source, product_id, phase (product/cover), run_id, kind, HTTP status nullable, sanitized message, attempted_at; primary key (source, product_id, phase) |
| covers | source, product_id, source_url, relative_path, downloaded_at; primary key (source, product_id) |

Source is `dtrpg` or `dmsguild`. All catalog reads, eligibility checks, failures, and covers are scoped to the selected source; writes derive source from the persisted run. Schema v2 transactionally upgrades v1, retaining all rows and assigning existing runs to DTRPG. The single unfinished-run index remains global. Migration can require significant disk space/time for copying a large catalog; failure rolls back the upgrade. Product success removes the matching product failure within the same commit. Cover success clears its cover failure. Failure records are batched, not a write per retry. Errors must not overwrite good product JSON. Successful cover metadata refers to the successfully downloaded URL, not merely the latest requested one.

Use WAL, one write owner, prepared upserts, a busy timeout, and short read transactions for UI queries. Use synchronous=NORMAL as the initial throughput-oriented setting: application-crash recovery remains valid, but a power/OS failure may lose recent committed transactions. Do not disable journaling or use synchronous=OFF. Explain that durability tradeoff in developer documentation, not as a setup obstacle.

Start batch thresholds at 1,000 product results OR 32 MiB accumulated response bytes OR 2 seconds since the first buffered result, whichever comes first. Flush at pause/end/phase completion. These values are provisional and must be measured. All write batches use transactions; do not perform HTTP/file downloads within them. A failed commit is not reported as catalogued; stop scheduling on persistence failure and retain the buffer if practical.

Database and runtime files live under an OS-appropriate application data directory. Permit choosing the data folder while idle; do not build a database configuration wizard. Protect the data folder against simultaneous app writers. Do not copy a live SQLite main file alone for export; a later exporter must use a consistent snapshot/backup operation.

Opening runs outside the UI lock and only inspects compatibility. It never
initializes, upgrades, or backfills a catalog. New/older catalogs remain open
with crawling disabled and a clear Data maintenance action. Explicit schema
updates run in the maintenance tab with stage-based progress and cancellation.
Close waits for rollback or an already-started commit and connection cleanup.
Failed folder switches keep the previous catalog open.

## 7. Fetching and response validation

Use one reused HTTP client/transport with bounded concurrency, timeouts, connection reuse, and context cancellation. Initial product concurrency: 8; keep it configurable internally/under advanced settings and tune from measurement. Bound channels and response bodies. Initial body limit: 16 MiB decoded; exceeding it records a clear failure rather than truncating JSON. Initial sitemap decoded limit: 64 MiB. Bound total in-flight and pending-write memory as well as batch size.

For each ID, request the direct API endpoint. Decode a small typed envelope to check that the response is a product, the product ID matches, and image information can be read. Accept the supplied JSON:API shape (data.type=Product and data.attributes.productId/image) and the verified live flat shape (top-level productId and image). Support for both was approved on 2026-11-01. Preserve the complete original response body. Do not treat arbitrary HTTP 200 JSON, an error object, HTML challenge, or an empty payload as a saved product. Unknown additional product fields remain acceptable.

At most 3 attempts per item per run by default. Retry timeouts and transient server failures with exponential backoff and jitter. Respect Retry-After and server rate limits. Record exhausted failures and continue other items. Confirmed authentication/session failure or challenge pauses the source and prompts reimport/Test connection. A single product restriction (including legacy product-specific 403 behavior) is an item outcome, not automatic proof that the session expired. Missing/removed products remain failures/outcomes; retain any prior good record.

## 8. Authentication compatibility

Port behavior from the old auth, HTTP, and Firefox code before simplifying internals. Preserve the actual request headers, cookies, origin/referer context, fixed User-Agent compatibility behavior, and any session/header derivation used there. The old adapter notes User-Agent-dependent HTTP 502 failures: do not replace that profile blindly.

Support Firefox profile selection and applicable container/session selection plus manual cookie paste. Preserve domain, path, secure, expiry, and host scope semantics. Read Firefox state using a safe snapshot/read mechanism; never mutate the browser profile. Keep credentials in private local credential storage, separate from catalog data, and redact diagnostics. Mask manual fields. Never log values or include them in exports or fixtures.

Test connection must exercise the real website/API request path and classify failures meaningfully. Report Ready only after a successful check; imported cookies alone do not mean validated access. Changed credentials invalidate the previous check. Authentication can expire during a run. Restrict redirects and never send website/API credentials to the cover CDN. Do not add CAPTCHA automation or unrelated browser automation.

## 9. Covers

After product fetching completes and writes flush, scan saved JSON in bounded pages and extract data.attributes.image (or top-level image in verified flat responses). Join the relative image path to the verified CDN base. Download one primary cover per product; ignore thumbnails, embedded description images, previews, and PDFs. Missing/null image means no cover available, not a failed product.

Use a separate bounded pool, initially 4 workers, and local deterministic paths such as covers/dtrpg/{product_id}/{url_hash}.{validated_extension}. Reject traversal and unapproved absolute URLs; paths on disk must be derived from safe IDs/hashes. Limit image bodies (initially 32 MiB) and validate response type/signature enough to reject an HTML error page.

Write a temporary file beside the destination, then atomically rename. Only afterward batch the successful cover metadata. A crash after rename but before metadata can cause harmless revalidation/redownload. Never record success before a complete file exists. Keep old files until replacement succeeds; cleanup of orphaned old files may be deferred.

Download if the file is missing or the desired URL differs from the last successful URL. Preserve an old cover on download failure. Same-URL image changes are deliberately not detected in v1. Missing covers/failed downloads are eligible next cover pass. Cover failures never mark the product JSON as failed. The product catalogued count is independent of cover completion.

## 10. UI contract

Full-window desktop workspace (redesign authorized 2026-11-06). A persistent sidebar contains identity, Crawler and Data maintenance navigation, the shared native storefront selector (DriveThruRPG / DM’s Guild), and Local data. The storefront selector remains locked during active or paused runs. The main workspace fills the available width, with its own scroll area and a shared bottom status bar. At narrow widths the sidebar becomes top navigation and content stacks without horizontal scrolling.

The Crawler has an explicit run-mode dropdown, optional Download covers after records, and one primary Start/Resume/Pause action in a command bar. A central run overview shows the current phase, progress and committed catalog/cover totals separately from queued products and errors this run. Phase indicators identify the held run's current phase and mark excluded steps for retries, cover-only runs and disabled cover downloads; they do not infer completed phases. Connection controls sit beside the run overview on wide windows. Connections and activity/errors expand in place. Data maintenance uses the same shell with separate metadata, progress, issues and database-structure sections. No storage-server setup, other storefront cards, catalog browser, or per-record animated log stream.

States: setup/sign-in needed, ready, discovering, fetching, downloading covers, pausing, paused, completed, completed with errors, and blocked by authentication/storage failure. Show the phase prominently. Before discovery finishes, show IDs found/sitemaps processed without a fabricated percentage. Afterward, show known product workload and run outcomes.

Queued means eligible product IDs not yet in flight. Catalogued means total distinct products successfully committed in the local catalog. For Refresh all, show run progress separately (saved this run / eligible total); catalogued may stay constant. Cover progress uses separate counts. Once failed attempts are exhausted they leave queued and appear as errors. A run with errors must not read as an unqualified success.

Emit aggregated progress at most 4 times/second; maintain counters in Go instead of repeatedly counting whole tables. Throughput is committed products/second, with covers separately labeled. Keep errors bounded in memory and pageable from persisted failures. Enable retry actions only when idle/paused under a clear run transition. Start is blocked by unvalidated connection or unavailable local storage; cover-only work does not require storefront cookies when CDN access works independently.

The original DTRPG mockup is illustrative and never reads browser credentials or contacts a storefront. Its demo state controls are not production application controls.

## 11. Implementation sequence and acceptance gate

1. Port and test authentication compatibility. Prove one real product request.
2. Implement sitemap discovery and deduplication with local fixtures.
3. Implement the minimal database and batched writer. Benchmark realistic JSON offline before network benchmarking.
4. Connect bounded fetch workers; validate pause, interrupted refresh, retry, and preservation of old data.
5. Implement cover pass and file/metadata recovery.
6. Bind the engine to Wails and implement the mockup's production interactions.

DTRPG is complete only when:

- A small authenticated live run discovers real sitemap IDs and retrieves matching product responses.
- Full response JSON including included resources survives storage unchanged.
- Repeated new-product runs skip saved products; Refresh all updates them without duplicates.
- A crash during a batch and an interrupted out-of-order refresh resume without skipping unfinished products.
- Missing/restricted products, rate limits, invalid JSON, expired auth, disk-full/commit errors, and incomplete discovery have correct visible outcomes.
- Covers download separately and partial files never appear as completed covers.
- The UI stays responsive and counters reflect commits, not received responses.
- Windows/Linux build and smoke-test results are documented; unavailable platforms are explicitly noted.
- A storage benchmark and a controlled end-to-end measurement report environment, concurrency, payload size, durability, HTTP timings, commit timings, and memory use. No unmeasured promise of 800 records/second.

This gate passed before the authorized DM’s Guild expansion. DM’s Guild uses the same recovery and isolation requirements; other storefronts remain out of scope.

## 12. Supplied example

fixtures/dtrpg-product-584455.json is a complete user-supplied response (~40 KB). It contains data.attributes.productId=584455, image=23458/584455.png, webImage=23458/584455.webp, and included Publisher, Category, and Filter resources. It is a test/example payload, not a schema for all possible products. Use it to prove lossless storage and minimal extraction, not to require every optional field on other products.

## 13. Structured metadata and explicit maintenance (2026-11-06)

Every extension of the data model must include a shared transformation for new
crawl responses and an explicit maintenance path for previously saved responses.
Schema version and transformation version are separate. Opening the app performs
neither structural updates nor transformations. Incompatible schemas block
crawling; missing historical metadata does not block new crawling once the
schema is compatible. A newer-than-supported schema requires a newer app.

The Data maintenance tab offers Apply database update, Extract metadata, and
Build D&D groups (section 14). Schema v3 adds the metadata tables to v2 without copying
products. The prior transactional v1-to-v2 copy remains available through this
explicit action. Initialization of an empty catalog is also explicit.

`internal/metadata` defines the fixed field mappings and one shared extractor
for flat responses and JSON:API responses with relationship-linked included
resources. `product_metadata` stores product and publisher scalar fields.
Separate product-scoped tables retain ordered creators, purchase options,
files, taxonomy entries, translated taxonomy labels, ancestors, and repeated
text values (description headers/footers, publisher URLs, integrations). All
keys include storefront and product ID. No author identity is inferred from a
name, and no cross-store deduplication occurs. Unknown additional fields, empty
API placeholders, and account/session-oriented values remain in the full JSON.

Extraction is conservative. Money is stored as exact decimal text without
rounding or conversion; no source currency is invented. Future product views
may display amounts as euros, per the user's display-only assumption. Page
counts and ratings retain their original strings alongside safely parsed
nullable values. Dates and opaque status codes retain their source encoding.
HTML is retained as text and must be sanitized before any future rendering.
The known sparse-object encoding of integration lists is handled in numeric
key order. Diagnostics contain fixed field names and reasons, not remote values.

Malformed individual fields produce bounded warnings while useful metadata is
committed. Warning-only results are complete and are not repeatedly processed.
A complete extraction failure still saves valid raw crawl JSON, records a
separate extraction failure, and retains the last good derived data as stale.
The `current_product_metadata` view excludes stale/failed/outdated projections.
Storage failures roll back the entire batch, including raw data and markers.

`metadata_state` tracks the extraction version, source response's fetched-at
marker, outcome and bounded diagnostics; each successful projection also keeps
its SHA-256. Raw data, derived data and the crawl success marker commit together.
Future mapping changes bump the transformation version and explicitly migrate
the current-data view target when needed (with a schema version bump); separate transformations
must get their own version and maintenance action instead of a generic job system.

Maintenance is local and source-scoped, uses the existing single write owner,
and runs exclusively with crawling. It reads at most 64 responses or about 4 MiB
per page (plus the final bounded response), extracts one response at a time,
and commits projections, outcomes and its sequential cursor atomically. The
normal crawl also extracts one response at a time inside its existing batch;
no HTTP or filesystem operations occur inside transactions. Response size is
limited to 16 MiB and derived rows to 20,000 per product. Stored field warnings
are capped at 32 per product, UI issue pages at 50 entries, and UI events at 4 Hz.

Progress counts committed outcomes, including failed extractions. Pause and
close cancel pending reads and let a current short write transaction settle.
After a restart, no work resumes automatically. Resume continues the persisted
maintenance pass; End retains committed work and releases the catalog. A later
pass retries failures and handles missing/outdated projections. Schema updates
show stages without a fabricated percentage; extraction has a known workload.

## 14. Materialized D&D groups (2026-11-06)

Fixed D&D edition grouping is now authorized for both storefronts. It is the
only classification added to this specification. See [grouping evidence and
rules](GROUPS.md) for verified source-specific taxonomy namespaces, all nine
edition mappings, ambiguity handling, and official-product precedence. B/X is
the broader Basic/BECMI/Rules Cyclopedia family by explicit user decision.

Schema v4 adds indexed group definitions/memberships and separate grouping
state/progress tables without copying or changing original responses. Data
maintenance offers **Build D&D groups** independently of **Extract metadata**.
Grouping uses the shared extractor with its own rule version, on both live
success and explicit historical maintenance. Its bounded batches, atomic cursor,
pause/resume/end, failure recovery, source locks and schema gate follow section 13.
Opening performs neither a schema update nor grouping. Current group reads seek
an indexed group range and point-check freshness; they never scan response JSON.
Ambiguous editions are reported, not guessed, and official status can be unknown.
