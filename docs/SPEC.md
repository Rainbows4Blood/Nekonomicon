# Nekonomicon — DriveThruRPG and DM’s Guild

Status: implementation specification. This replaces the previous design entirely.

## 1. Goal and scope

Build a slick, simple, fast local desktop crawler in Go/Wails. Collect DriveThruRPG and DM’s Guild product API responses and primary covers. The application runs on the user's Windows or Linux machine without a database server or object-storage service.

The DTRPG gate passed on 2026-11-01; the user authorized DM’s Guild on 2026-11-05. Provide a selector on the Crawler page for these two implemented storefronts only. Storytellers Vault and Ulisses eBooks remain deferred. D&D browsing/search and book details are authorized as of 2026-11-07 (section 15). No general classification system; the specific adventure enrichments are authorized in section 17. No (fixed D&D groups are specified in section 14), cross-store deduplication, network storage, scheduler, or distributed architecture.

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

Source is `dtrpg` or `dmsguild`. Crawler catalog reads, eligibility checks, failures, and covers are scoped to the selected source; Data maintenance covers the whole database; writes derive source from the persisted run. Schema v2 transactionally upgrades v1, retaining all rows and assigning existing runs to DTRPG. The single unfinished-run index remains global. Migration can require significant disk space/time for copying a large catalog; failure rolls back the upgrade. Product success removes the matching product failure within the same commit. Cover success clears its cover failure. Failure records are batched, not a write per retry. Errors must not overwrite good product JSON. Successful cover metadata refers to the successfully downloaded URL, not merely the latest requested one.

Use WAL, one write owner, prepared upserts, a busy timeout, and short read transactions for UI queries. Use synchronous=NORMAL as the initial throughput-oriented setting: application-crash recovery remains valid, but a power/OS failure may lose recent committed transactions. Do not disable journaling or use synchronous=OFF. Explain that durability tradeoff in developer documentation, not as a setup obstacle.

Start batch thresholds at 1,000 product results OR 32 MiB accumulated response bytes OR 2 seconds since the first buffered result, whichever comes first. Flush at pause/end/phase completion. These values are provisional and must be measured. All write batches use transactions; do not perform HTTP/file downloads within them. A failed commit is not reported as catalogued; stop scheduling on persistence failure and retain the buffer if practical.

Database and runtime files live under an OS-appropriate application data directory. Permit choosing the data folder while idle; do not build a database configuration wizard. Protect the data folder against simultaneous app writers. Do not copy a live SQLite main file alone for export; a later exporter must use a consistent snapshot/backup operation.

Opening runs outside the UI lock and only inspects compatibility. It never
initializes, upgrades, or backfills a catalog. New/older catalogs remain open
with crawling disabled and a clear Data maintenance action. Explicit schema
updates run in the maintenance tab with stage-based progress and cancellation.
Close waits for rollback or an already-started commit and connection cleanup.
Failed folder switches keep the previous catalog open.
All transitive frontend modules must be embedded in the executable. A small startup
script wires safe closing before loading the main module; module failures display
an interface error instead of an indefinite catalog-loading spinner. Abort and
close remains valid after catalog opening finishes, using the normal shutdown path.

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

Full-window desktop workspace (redesign authorized 2026-11-06). A persistent sidebar contains identity, Crawler and Data maintenance navigation, and Local data. The native storefront selector (DriveThruRPG / DM’s Guild) belongs only to the Crawler page. The storefront selector remains locked during active or paused runs. The main workspace fills the available width, with its own scroll area and a shared bottom status bar. At narrow widths the sidebar becomes top navigation and content stacks without horizontal scrolling.

The Crawler has an explicit run-mode dropdown, optional Download covers after records, and one primary Start/Resume/Pause action in a command bar. A central run overview shows the current phase, progress and committed catalog/cover totals separately from queued products and errors this run. Phase indicators identify the held run's current phase and mark excluded steps for retries, cover-only runs and disabled cover downloads; they do not infer completed phases. Connection controls sit beside the run overview on wide windows. Connections and activity/errors expand in place. Data maintenance uses the same shell with separate metadata, progress, issues and database-structure sections. No storage-server setup, other storefront cards, or per-record animated log stream.

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

Maintenance is local and database-wide across both storefronts, uses the existing single write owner,
and runs exclusively with crawling. It reads at most 64 responses or about 4 MiB
per page (plus the final bounded response), extracts one response at a time,
and commits projections, outcomes and each storefront’s sequential cursor atomically.
Both storefront checkpoints are claimed together and remain held until the whole
pass finishes or is ended. Progress, counts, and paginated issues cover both stores;
issues retain storefront identity. Resuming an older storefront-only pass keeps
its cursor and includes pending work from the other storefront. Opening performs
no changes and maintenance never changes the selected Crawler storefront. The
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

## 15. D&D library and browsing index (2026-11-07)

A third workspace, D&D, lists the products assigned to the nine edition groups
across both stores. It has one edition filter, three official-status choices
(All, Official only, Non-official only), Name / Release date ascending/descending,
50-row keyset pages, and full-text Name only / Name & description search. Unknown
official status is included only by All. Overlapping edition memberships do not
duplicate a book in All editions; source/product identities remain independent.
Release date means the original dateAvailable value. Recognized ISO dates and
storefront timestamps sort by their calendar day without timezone/date guessing;
missing/unrecognized dates sort last in both directions. Name ordering uses Unicode
case and accent folding, with source/product ID as a stable tie breaker.

Schema v5 explicitly creates catalog_state/catalog_jobs, dnd_books, dnd_listing,
FTS5 dnd_search and six indexes supporting each edition/official/sort combination.
The empty edition is one all-editions listing entry; each book additionally has
one entry per supported edition. A separate **Build D&D browsing index** action
fills this read model from original responses through the shared extractor and
grouping rules, independently of the older maintenance tasks. It uses the same
bounded pages, single writer, atomic result/cursor commits, recovery, task locks
and database-wide ownership. Opening only inspects; schema setup never backfills.
Live product success writes raw data, metadata, groups and browsing/search index
in the same transaction. Failed refreshes preserve last good data; failed
extractions keep older derived data stale and exclude it from browsing/search.
Non-D&D products are marked indexed but do not produce book/listing/FTS rows.
Changes to the extractor, grouping rules or browsing projection must bump the
independent catalog transformation version and provide explicit index maintenance.

FTS uses the SQLite unicode61 tokenizer with accent removal. User input is split
into literal words joined with AND; raw FTS operators are not accepted. Search
is bounded to 512 input bytes and 16 words. Name-only constrains the FTS column;
name/description searches both columns. Ordinary browsing seeks B-tree indexes;
full-text searches start with FTS matches, point-join book/listing/state keys and
sort the matched subset. No product JSON is scanned to render a list page.

Clicking a row opens a dedicated details view within the D&D workspace. It reads
one saved response and uses the same extractor. Back preserves list controls,
page and focus. Displayed descriptions are converted to plain paragraphs: scripts,
styles, embedded content and remote images never render. Name/publisher fields
are bounded to 4 KiB, description display/search to 1 MiB (with a warning), and
each details collection to 100 entries. Full original JSON remains unchanged.
Available metadata includes publisher, dates, creators, formats/files, page count,
prices, ISBN and ratings. Euro display follows the existing user decision.

Covers are served only from recorded, canonical product-local paths under the
catalog root, with catalog identity checked on requests and rooted file access.
Thumbnail decoding is limited to two concurrent operations, a 20 MiB input and
16 million pixels; oversized/unavailable images fall back to a placeholder.
List/detail thumbnails fit 120×170 / 400×560 respectively. Reads and image work
never occur inside a write transaction; no remote cover loading is added.

Switching to D&D changes the whole shell into a high-fantasy library: dark green
sidebar, gilded accents, parchment surfaces, serif titles and a locally embedded
painted library header. Leaving it restores the original shell. Native controls,
visible focus, responsive layouts, tab keyboard navigation and reduced-motion
preferences remain intact. The active crawling storefront selector remains local
to Crawler. D&D has its own independent Both / DriveThruRPG / DM’s Guild filter.


### Storefront filter and count recovery (2026-11-07)

The D&D source filter is independent of Crawler selection and combines with both
search scopes, edition, official status, all sort directions and keyset paging.
Changing source resets paging and invalidates cursors from a different source.
Schema v6 explicitly adds six edition/source/sort indexes (with official-status
variants) and products_freshness(source, product_id, fetched_at). SQLite builds
these over existing rows during Apply database update; no response re-extraction
is needed. Live writes maintain them through the same single writer.

Summary queries use the covering freshness index, avoiding large response JSON
pages. Each completion-time summary gets a separate bounded read context. Failed
count reads flag the displayed counts as stale but never override a committed task
result or create an artificial held task. Refresh counts retries these reads alone.
Actual write/status-persistence failures still pause or block recoverably.

## 16. OpenAI connection (2026-11-07)

OpenAI authentication supplies the connection for section 17 enrichment. Signing
in alone does not send catalog records. Follow OpenAI's documented Sign in with ChatGPT
public-client flow for open-source apps: system browser, dynamic client registration,
stable installation host ID, issued client IDs on subsequent sign-ins, random state,
OIDC nonce, PKCE S256, and a short-lived 127.0.0.1 loopback callback. The exact callback
path is /auth/callback; only its port varies. Never use another product's client ID,
import Codex credentials or collect a password in the frontend.

Verify RS256 signatures with OpenAI JWKS, issuer, issued-client audience, expiry,
nonce and account identity before activation. Plan permission requires the granted
resource.invoke and chatgpt.tokens.use.direct scopes; identity alone is insufficient.
Keep registrations separate, retain issued IDs through failed code exchange and
logout, and show a stable label for each saved account. Only one account is active.

Store tokens in an atomic AES-GCM encrypted config file, with a dedicated OS
credential-store key. Serialize file access/refresh across app instances with a
local lock. Tokens and authorization URLs never cross the Wails bridge, enter the
catalog, or appear in logs. Bound HTTP time and size; reject redirects on token,
JWKS and revocation requests. Cancellation and shutdown close the pending listener.
Sign-out attempts revocation via the discovered same-origin endpoint with one
bounded retry; clear local tokens and disclose unconfirmed remote revocation.

The Go-side credential provider renews near expiry and saves rotating tokens
before use. It is not exposed to the frontend. Model integration must
respect the selected API's contract; this login supplies OAuth access to eligible
Responses requests, not an unrestricted API key. The current ChatGPT-plan preview
requires streaming, store:false and array input and rejects several default API
fields/tools. No SDK runner or extra runtime is added in this authentication step.

Official references: [registration and sign-in](https://developers.openai.com/siwc/token-sharing-open-source/sign-in),
[accounts and sessions](https://developers.openai.com/siwc/token-sharing-open-source/profiles-and-sessions),
and [preview limitations](https://developers.openai.com/siwc/token-sharing-open-source/preview-limitations).


## 17. D&D adventure enrichment (2026-11-07)

D&D contains Library and Enrichment subtabs, both under the fantasy theme. One
Enrich action runs all currently missing eligible enrichments across the database;
Library filters do not scope it. The five fixed enrichments are player range
(min_player, max_player, excluding GM), character levels (min_level, max_level),
settings, locations and in-world dates.
They apply only to current indexed D&D books counted as adventures by source taxonomy.

Schema v7 adds dnd_books.is_adventure with a source/product eligibility index and
an enrichment_results table separate from original product data. Catalog projection
v2 derives eligibility during live crawling and the existing explicit Build D&D
browsing index path. Opening never backfills. Schema update alone cannot identify
old adventures; run index maintenance afterward. A bounded read-only taxonomy audit
confirmed filter 2110 (parent 2150/group 1) in both stores, plus DM's Guild 44747
(parent 44736/group 27) and 45393 (parent 45341/group 29). These mean Campaigns,
Adventures & Modules / Adventures. Exact adventure labels in filter descriptions
also qualify. Product titles and publisher/category labels do not confer eligibility.

Use exactly gpt-6.1-terra with service_tier=priority (fast mode) for both passes and confirm account
availability before processing. The UI describes fast mode as requested, since
OpenAI can return a different processing tier. Never
substitute a model. Each request sends the entire original JSON, including included
resources, and a data-URL JPEG of the complete local cover at up to 1600 pixels.
Input JSON is bounded to 16 MiB without truncation; cover input to 20 MiB/16 MP.
Missing, unsafe or unsupported covers produce a recoverable issue and no inference.
No authenticated image URL or storefront cookie is sent to OpenAI.

Two passes enforce source-first behavior: the first exposes no tools; if requested
endpoints or context facts remain unknown, a second exposes web_search with auto tool choice. Its prompt
permits searching only missing, unfinished enrichments, using product identity rather than full
JSON as search terms. Both use strict structured output with nullable integer ranges and context lists,
short supporting evidence and web-source URLs. Treat all source content as untrusted
data, never instructions. Never infer normal party sizes or level caps. Known values
are immutable within a source revision; returned conflicting ranges are rejected.

Use streaming Responses with store:false and array input under the existing OAuth
connection. A complete, validated response.completed event is required; refusals,
failed/incomplete streams and malformed values cannot become successful results.
The model receives complete storefront evidence on either pass. Preserve storefront
results before web fallback so cancellation/failure cannot lose them. Save each
validated update through the one database writer with a source-freshness check.
Parse structured JSON from the final assistant message, never by concatenating
commentary with the answer. Prefer phase=final_answer; when phase is absent, use
the last unphased assistant message. Commentary and tool results cannot complete
enrichment. Still reject missing required fields, refusal, invalid ranges and
unfinished final messages; explicit null endpoints in valid JSON are successful.
Retain response.output_item.done messages by output_index until the terminal event;
ChatGPT-plan responses can have an empty response.completed.output even after
streaming a valid final answer. Reconcile any terminal items at the same positions,
deduplicate repeated events and order messages by their output positions. Never
commit an item without successful response.completed. Bound retained messages to
256 in addition to the existing total stream and event-size limits.

Sixteen cancellable book workers with unbuffered dispatch and commit channels,
two sequential enrichment passes per visited book, no network in transactions.
HTTP timeout is three minutes per attempt; streamed input is bounded to 32 MiB and
events to 8 MiB. Explicit OpenAI usage/rate-limit rejections (HTTP 429 or structured
stream errors) pause new requests across all workers. Retry the same request with
exponential backoff starting at 2 seconds, adding jitter and capping the delay at
5 minutes. Honor longer Retry-After seconds or HTTP-date deadlines. One request
probes availability after each delay; other workers wait until the probe resolves.
Concurrent rejected requests do not multiply the backoff, and older in-flight
successes do not clear it. A successful probe restores normal parallel work.
As requested by the user, these retries continue until recovery or Stop, with no
attempt limit; each wait is cancellable and outside the request timeout. Retrying
does not repeat a completed storefront pass, erase checkpoints, increment web-pass
counts, or save an issue. Show waiting books and retry times in the dashboard.
No other automatic inference retries. Authentication, model and other service
rejections stop the run; individual cover/output issues remain visible and permit
other books to continue. Each unfinished book is visited
at most once per pass. Stopping/shutdown closes in-flight work and retains commits.
On restart, selecting unfinished enrichments recovers progress without a separate
job queue. A single coordinator commits every validated checkpoint before its worker
continues. Out-of-order completion never advances a persistent catalog cursor.
Authentication refresh/storage is serialized independently of model requests.
Global failures cancel in-flight requests and stop dispatch; shutdown joins all work.

Persist nullable numeric columns, both ranges' evidence, model/version, source
fetched timestamp, cover hash, update timestamp and outcome/error. Complete fields
are skipped. Schema v8 adds separate players_done and levels_done markers. A
successful storefront pass completes fully known ranges; a successful fallback
completes requested ranges even with partially or entirely null endpoints. Unknown
values are normal enriched outcomes, never operational issues. Failed/interrupted
requests remain eligible, preserving checkpoints and already finished ranges.
The explicit, atomic database update backfills completion for legacy known ranges.
Legacy unknown results need one follow-up because v7 cannot distinguish a completed
fallback from a storefront checkpoint. Opening only inspects compatibility; no
index rebuild is required when upgrading v7 to v8. A new source
revision makes old results stale; maintenance rebuilding the browsing projection
preserves enrichment. Dashboard counts and 30-row result pages read current results;
book details display ranges, completed unknowns and evidence. No catalog JSON, credentials or raw model
streams are placed in frontend events/logs. Only explicit Enrich sends catalog data.

Official API references: [GPT-6.1 Terra](https://developers.openai.com/api/docs/models/gpt-6.1-terra),
[web search](https://developers.openai.com/api/docs/guides/tools-web-search),
[structured outputs](https://developers.openai.com/api/docs/guides/structured-outputs),
[ChatGPT-plan inference](https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference).

### Setting, location and date enrichments (2026-11-08)

Schema v9 adds separate JSON/evidence, known-value flags and completion markers
for settings, locations and dates in enrichment_results. Apply database update is
explicit, transactional and cancellable. Preserve v8 player/level values and done
markers, including completed unknowns; mark the new enrichments pending and old
complete rows partial. No index rebuild is needed. Result format version remains
1 because this extension is additive and completion is tracked per enrichment.
Newly crawled/refreshed adventures become eligible through the existing shared
projection; a refreshed source revision invalidates all five older enrichments.

Settings contain multiple names plus an independent agnostic flag, evidence and
sources. Agnostic requires explicit setting-neutral/adaptable-to-any-setting text
and can coexist with named settings. Empty/null names with agnostic=false means
unknown, not a claim that the adventure cannot be adapted. Game-system compatibility
alone is not setting evidence. Locations contain named adventure sites and nullable
setting associations; exclude passing background mentions and publisher addresses.
Dates contain source-text fictional dates/ranges and nullable calendar/setting
associations. Preserve approximate, seasonal, relative or alternative dates without
inventing precision or conversions. Never use publication dates, web timestamps,
edition release dates or assumed default campaign years as adventure dates.

The existing source-first and web-fallback rules apply independently to each of
the five enrichments. Save known storefront facts before researching missing ones;
preserve finished facts and completed unknowns across retries. Successful fallback
finishes requested enrichments even with empty/null lists. Missing required JSON
objects/fields, invalid data and failed requests do not complete enrichments.
Bound settings to 16 names, locations to 64, dates to 32, each label to 512 bytes,
and evidence/source lists to existing limits. Show five completion counts plus
eligible/pending book counts, all facts and evidence in dashboard and details, and
distinguish not-yet-enriched from completed unknown context fields.
