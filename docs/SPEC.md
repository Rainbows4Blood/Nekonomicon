# Nekonomicon v1 — DTRPG first

Status: implementation specification. This replaces the previous design entirely.

## 1. Goal and scope

Build a slick, simple, fast local desktop crawler in Go/Wails. Collect DriveThruRPG product API responses and primary covers. The application runs on the user's Windows or Linux machine without a database server or object-storage service.

DTRPG must work before DMs Guild, Storytellers Vault, or Ulisses eBooks are implemented. No source selector or future-storefront placeholders in v1. No browsing/search/classification/enrichment system, cross-store deduplication, network storage, scheduler, or distributed architecture.

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
- Primary image base: https://d1vzi28wh99zvq.cloudfront.net/

Reference files:

- [Adapter and response classification](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs)
- [Sitemap parser](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/sitemap.rs)
- [HTTP compatibility and robots](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs)
- [Authentication](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth.rs)
- [Firefox session import](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth/firefox_session.rs)

These are behavioral references, not a requirement to copy the old queue/storage/parser architecture. Confirm the protocol with a small live authenticated smoke check during implementation; these endpoint details are observed code, not an external API guarantee.

## 4. Discovery

Load robots.txt for website and API origins, preserving existing policy and header behavior. Extract declared Sitemap URLs. If there are no approved declarations, use the fixed, verified https://www.drivethrurpg.com/sitemap.xml entry point and visibly report this fallback. This exception was approved on 2026-11-01 after the live robots response omitted sitemap declarations and the legacy root yielded matching products. Robots rules still apply; do not guess other roots. Follow sitemapindex entries recursively; parse urlset product entries. Support plain XML, namespaces, escaped URLs, and gzip. Ignore extension entries such as image:loc. Validate XML completeness and enforce decoded-size limits.

Maintain visited sitemap URLs and a deduplicated set of positive product IDs. Accept modern /product/{id}, localized /en/product/{id}, and legacy /product_info.php?products_id={id} forms supported by the old adapter. Language/slug variants resolve to one ID. Reject unrelated hosts and non-product URLs. Preserve optional lastmod as a hint only; v1 does not depend on it to skip updates.

Sitemap failure after bounded retries makes discovery incomplete. Show the failure and offer retry; do not silently proceed as a complete crawl. An unavailable declared/fallback sitemap or zero IDs is a visible diagnostic, not an automatic successful empty crawl. Never delete saved products because a sitemap omits them. Do not add listing pagination or numeric-ID guessing.

A fresh process may rediscover sitemaps rather than persist an exact frontier. In-memory pause can retain the frontier. The discovered total is a snapshot and does not prove that all existing/private products are listed.

## 5. Modes and recovery

- **New products** (default): rediscover, then fetch IDs without a successfully saved product response. Existing failures remain eligible.
- **Refresh all**: rediscover, then fetch all IDs, updating successful responses in place. Retain the last good response if a refresh fails.
- **Retry failed**: retry recorded product failures; no full sitemap pass needed. Cover failures belong to the cover pass.
- **Download covers**: scan saved products for missing or changed primary covers; no product requests needed.

Create a run ID and persist mode, start time, state, and covers preference. Record last_success_run_id on each product in the same transaction as its JSON. Resume an interrupted refresh using the same run ID and omit already-committed successes. A new run gets a new ID. Failures do not set this success marker.

Only one active run. If an unfinished run exists, show Resume and End run; do not silently start another. Ending a run keeps all committed data; a future refresh starts a new run. Resuming after a process restart rediscovers IDs where needed, so the source set can differ from the initial snapshot. Do not promise an immutable frontier or exact recovery of volatile progress counters.

Pause stops scheduling, lets bounded in-flight requests settle, flushes successful buffers, and then reports Paused. Resume continues that run. Stop/end flushes and closes the run when possible. Abrupt crash may lose the current buffer and queue state; replay is acceptable. Out-of-order worker completion must not skip unfinished IDs: never resume using the largest completed ID or a single frontier cursor.

## 6. Minimal SQLite model

One local SQLite database. Use ordinary JSON TEXT containing the entire API response, including top-level data, relationships, and included. No normalized author/publisher/filter tables in v1. Do not transform and reserialize the response merely to store it.

Suggested tables (implementation may refine types without expanding scope):

| Table | Fields and constraints |
| --- | --- |
| products | source TEXT, product_id INTEGER, fetched_at UTC timestamp, last_success_run_id, response_json TEXT NOT NULL; primary key (source, product_id) |
| runs | id primary key, mode, phase, state, started_at, finished_at nullable, download_covers |
| failures | source, product_id, phase (product/cover), run_id, kind, HTTP status nullable, sanitized message, attempted_at; primary key (source, product_id, phase) |
| covers | source, product_id, source_url, relative_path, downloaded_at; primary key (source, product_id) |

Source is fixed to dtrpg in v1; the column costs little and keeps exports unambiguous. Product success removes the matching product failure within the same commit. Cover success clears its cover failure. Failure records are batched, not a write per retry. Errors must not overwrite good product JSON. Successful cover metadata refers to the successfully downloaded URL, not merely the latest requested one.

Use WAL, one write owner, prepared upserts, a busy timeout, and short read transactions for UI queries. Use synchronous=NORMAL as the initial throughput-oriented setting: application-crash recovery remains valid, but a power/OS failure may lose recent committed transactions. Do not disable journaling or use synchronous=OFF. Explain that durability tradeoff in developer documentation, not as a setup obstacle.

Start batch thresholds at 1,000 product results OR 32 MiB accumulated response bytes OR 2 seconds since the first buffered result, whichever comes first. Flush at pause/end/phase completion. These values are provisional and must be measured. All write batches use transactions; do not perform HTTP/file downloads within them. A failed commit is not reported as catalogued; stop scheduling on persistence failure and retain the buffer if practical.

Database and runtime files live under an OS-appropriate application data directory. Permit choosing the data folder while idle; do not build a database configuration wizard. Protect the data folder against simultaneous app writers. Do not copy a live SQLite main file alone for export; a later exporter must use a consistent snapshot/backup operation.

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

A single crawler screen, DTRPG only. Quiet desktop layout with compact identity header, explicit run-mode dropdown, optional Download covers after records, and one primary Start/Resume/Pause action. Connections and activity/errors expand in place. No storage-server setup, other storefront cards, catalog browser, or per-record animated log stream.

States: setup/sign-in needed, ready, discovering, fetching, downloading covers, pausing, paused, completed, completed with errors, and blocked by authentication/storage failure. Show the phase prominently. Before discovery finishes, show IDs found/sitemaps processed without a fabricated percentage. Afterward, show known product workload and run outcomes.

Queued means eligible product IDs not yet in flight. Catalogued means total distinct products successfully committed in the local catalog. For Refresh all, show run progress separately (saved this run / eligible total); catalogued may stay constant. Cover progress uses separate counts. Once failed attempts are exhausted they leave queued and appear as errors. A run with errors must not read as an unqualified success.

Emit aggregated progress at most 4 times/second; maintain counters in Go instead of repeatedly counting whole tables. Throughput is committed products/second, with covers separately labeled. Keep errors bounded in memory and pageable from persisted failures. Enable retry actions only when idle/paused under a clear run transition. Start is blocked by unvalidated connection or unavailable local storage; cover-only work does not require DTRPG cookies when CDN access works independently.

The mockup is illustrative and never reads browser credentials or contacts DTRPG. Its demo state controls are not production application controls.

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

Only then consider the other storefronts. No work on them is required to finish this specification.

## 12. Supplied example

fixtures/dtrpg-product-584455.json is a complete user-supplied response (~40 KB). It contains data.attributes.productId=584455, image=23458/584455.png, webImage=23458/584455.webp, and included Publisher, Category, and Filter resources. It is a test/example payload, not a schema for all possible products. Use it to prove lossless storage and minimal extraction, not to require every optional field on other products.
