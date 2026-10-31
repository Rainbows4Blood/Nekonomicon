# Nekonomicon crawler specification

**Status:** Authoritative requirements for the Go + Wails reimplementation, updated 31 October 2026.

**Source behaviour:** The [KaijuCrawl Rust crawler reference](reference/KaijuCrawl_Rust_Crawler_Reference.md) preserves the inspected implementation at [f2ced97ac62f24e32abd51fd10b88fb7bda344af](https://github.com/Rainbows4Blood/kaijucrawl/commit/f2ced97ac62f24e32abd51fd10b88fb7bda344af). Only the target, HTTP, cookie and pure validation behaviours explicitly retained below apply to Nekonomicon. Its SQLite queue, local archives, transactions, per-job durability, ownership fencing and old batch sizes are historical.

**Scope:** Crawl and retain complete product responses for the selected storefronts, then download their primary cover images. This specification does not implement catalogue browsing, AI analysis or classification. The design handoff and mockups are in [docs/design](design/README.md).

## 1. Operating contract

1. Store the complete accepted JSON response, including long HTML descriptions, in MongoDB, with one product collection per crawl target.
2. Use ordinary single-document atomic writes and acknowledged bulk operations. **No explicit multi-document MongoDB transactions are required or used by the crawler.** Standalone MongoDB and replica-set deployments are supported, locally or remotely.
3. Keep discovery, scheduling, deduplication, attempts and pending captures primarily in memory. Periodically save compact recovery checkpoints; a durable per-job ledger is not required.
4. Accumulate validated products into batches of **up to 1,000 records**, with byte, memory and age bounds. A crash may discard unflushed captures and queue changes since the last usable checkpoint.
5. Complete the document-capture phase before starting the image phase. Image work is derived from saved product documents, rather than atomically enqueued in a separate job collection.
6. Keep downloaded image bytes in S3. Record image results independently with conditional updates to their product documents.
7. Retry and rediscovery may repeat work. Stable product keys and revision-aware updates must preserve already saved data. Do not advertise exactly-once processing, a complete audit trail, lossless queue recovery or strict stale-owner exclusion.

This is deliberately a best-effort ingestion pipeline. Accepted loss concerns **volatile work and progress**, not permission to truncate records, discard acknowledged products, disable write acknowledgement or report buffered records as saved. A crash can interrupt a bulk operation after only some records have been stored.

## 2. Storage and deployment

| Data | Location and role |
| --- | --- |
| Complete accepted product response and capture metadata | MongoDB product collection for that target; authoritative saved data |
| Primary image URL, state, capture revision and stored S3 reference | The same product document |
| Primary cover-image bytes | Configured S3 bucket and prefix |
| Discovery frontier, active work, retry scheduling, deduplication and unflushed captures | Memory during the run |
| Run/phase summaries, compact queue snapshots, recent failures and known cooldowns | Small MongoDB records/checkpoint chunks; recovery aids, not an authoritative job ledger |
| Catalogue identity and optional advisory active-run lease | Small MongoDB records; never updated for each product |
| Connection settings | Local configuration; credentials in the OS credential store or another protected provider |

Use one product collection each for DriveThruRPG, DMs Guild, Storytellers Vault and Ulisses ebooks; future targets get their own collections. A product key includes catalogue identity, target and normalized source product ID. Enforce uniqueness within the target collection, for example with a unique index on `(catalogue_id, product_id)` or an equivalent deterministic `_id`. Do not merge cross-storefront listings automatically.

Retain the source response as queryable BSON. Do not spill JSON into S3, GridFS, local archives or another store, and do not duplicate the entire HTML field merely to create a second representation. Derived query fields are optional and should be limited to actual application needs.

Use a single long-lived MongoDB client and a bounded writer. Build required indexes at setup, not during product processing. Index identity and the actual image-selection/query predicates; do not index every arbitrary source field. Optional search indexes are a separate feature.

### Acknowledgement and durability

Keep writes acknowledged. The default product/image write policy is `w: 1, j: true` for a standalone deployment and `w: "majority", j: true` for a replica set. Use bounded operation timeouts. These are ordinary write concerns, not transactions; no replica set must be created solely for crawling.

An acknowledgement timeout or lost connection can leave an **unknown** result even when some writes reached the server. Reconcile or safely repeat those operations. Do not equate a timeout with “nothing was written,” and do not count queue admission or request submission as persistence. Checkpoint loss has the weaker recovery semantics described below.

References: [single-document atomicity](https://www.mongodb.com/docs/manual/core/write-operations-atomicity/), [bulk operations](https://www.mongodb.com/docs/manual/core/bulk-write-operations/), [write concern](https://www.mongodb.com/docs/manual/reference/write-concern/).

## 3. Configuration and crawl gate

MongoDB and S3 remain mandatory saved configuration prerequisites for Start and Resume. Keep the existing Storage screen:

- **MongoDB:** connection string and database name. Support standalone and replica-set deployments. Preflight must not require transactions or transaction-capable topology.
- **S3:** bucket, region, optional object-key prefix and optional custom endpoint; AWS default credential provider chain or explicit access key/secret, with optional session token.

Protect and redact credentials, including credentials inside a MongoDB URI. Connection tests are optional diagnostics; a saved form or successful ping does not prove permissions. A test must not mutate product data. If the S3 test uses a temporary uniquely named object, use no crawled data and clean it up.

Enable Start only when both configurations are syntactically complete and at least one target is selected. The backend enforces the same gate. At start/resume, verify MongoDB connectivity and necessary read/write/index permissions, and verify access to the configured S3 destination. Report actionable failures before starting storefront requests. Do not silently fall back to SQLite, local product files or local image storage.

Use a stable destination snapshot for the run. Do not switch database, bucket, endpoint or prefix in flight. Store sufficient non-secret destination information in each saved image reference; changing settings must not reinterpret old references. Moving existing data is an explicit migration, outside this change.

If MongoDB becomes unavailable, stop admitting work before bounded buffers overflow and pause persistence. If S3 becomes unavailable after preflight, already saved metadata remains valid; the image phase pauses with pending work recoverable from product documents. An S3 problem must not roll back metadata or force refetching it. Do not introduce S3 calls into the metadata write path.

## 4. Product document and failure boundary

### Accepted response

Retain the **entire accepted per-product JSON response**, including its outer envelope, unknown properties, nested data and original HTML. A top-level array stays an array in the source value. Parsing to BSON preserves information, not byte-identical whitespace or key order.

Maintain exact numeric values. Use a documented lossless representation when BSON numeric types cannot represent a source number exactly, or fail explicitly; never silently round or drop fields. Existing target identity, response-classification and full-description completeness checks remain in force.

A minimal application envelope contains:

| Field group | Contents |
| --- | --- |
| Identity | Catalogue, target, normalized product ID, canonical/source API URL |
| Source | Complete accepted response as a BSON value |
| Capture | Capture revision, original observation time, safe HTTP metadata, parser/schema version, complete-capture indicator |
| Image | Primary source URL, state, revision associated with that URL, and an optional verified S3 reference |
| Optional derived metadata | Only fields needed for browsing, filtering or reprocessing |

A capture revision is an application-generated identifier attached to a prepared capture and retained across retries. It need not be a sortable source revision or source timestamp. Image completions refer to that saved revision.

Save source data, capture metadata and the initial image state in **one document operation**. Use `pending` when a primary image exists and `not_available` when it does not. No separate image-job insert or product-job completion must commit atomically with it.

### Record-size and validation failures

The complete encoded BSON document must fit MongoDB's **16 MiB per-document limit**, including the envelope and image metadata. Check the encoded document, not just the downloaded JSON. The existing 16 MiB HTTP limit still applies before conversion; an HTTP size rejection is a different error.

If a response fails identity/completeness/encoding checks or exceeds the document limit:

- Do not truncate, split, compress-to-bypass, or spill the product payload elsewhere.
- Mark the item failed in the current in-memory run with a specific reason, including `mongodb_document_too_large` for the BSON limit.
- Include only identity, observation time, error and measured sizes in the small failure buffer/checkpoint. Batch these diagnostic writes; no synchronous error ledger is required for every item.
- Keep any previously saved complete product and image reference intact.
- Do not retry a deterministic failure in the same uninterrupted run. It can be attempted again in a later run.
- A crash before the next diagnostic checkpoint can lose that failure status. Rediscovery may try the item again; this is an accepted best-effort tradeoff.

Failure-only records must not masquerade as complete products. Prefer a separate compact issues/checkpoint collection; if legacy failure stubs exist in a product collection, they never qualify for incremental-skip logic.

The oversized body is intentionally not retained. A MongoDB outage also prevents durable failure recording; report the local issue and pause as needed, without claiming it was saved. See [MongoDB document limits](https://www.mongodb.com/docs/manual/core/document/#document-size-limit).

## 5. In-memory discovery and scheduling

Maintain compact queues/sets for sitemap URLs, product identities, active workers, retry due times and recent failures. Keep payloads in the bounded capture buffers, not in every queue entry. One uninterrupted run admits at most one active/buffered metadata task per product identity.

Discovery adds validated identities to memory. It does not insert a MongoDB job, claim, attempt record or discovery row for every item. Deduplicate aliases and sitemap cycles in memory. Preserve the original source URL/provenance as useful compact metadata, without requiring a durable per-sighting audit trail.

For incremental crawling, load saved complete product identities in bounded projected/keyset pages, or test discovered identities in bounded indexed groups. Avoid one existence query per discovered ID. Never skip an item merely because an old queue checkpoint says “done”; saved complete product data determines whether capture is already available.

Normal crawling retains the historical first-successful-capture behaviour: scan sitemaps on each fresh run, skip complete saved products, and retry unsuccessful products in later runs. A changed sitemap `lastmod` alone does not trigger a refresh. The full-recrawl overwrite/history policy remains an explicit open decision in section 14.

Discovery and metadata fetching can run concurrently. Limit both worker counts and memory. Apply backpressure when the queue or capture buffers are full; discovery must yield capacity so product workers can drain the queue. Do not silently discard live-run work to keep admitting more requests. No local spool database is required.

Retain bounded retries and HTTP/robots pacing from section 12. Attempt increments, queue moves and progress counters are in-memory operations. Checkpointed attempt counts may be stale after a crash; an uninterrupted pause/resume retains its in-memory budget, while restart recovery can repeat work. Do not promise exact lifetime-attempt accounting.

## 6. Bulk metadata persistence

### Default internal tuning values

These are implementation defaults, not additional mandatory UI settings.

| Setting | Default |
| --- | ---: |
| Product records per target-collection bulk | Up to 1,000 |
| Encoded product payload per bulk | Up to 16 MiB |
| Maximum age of a nonempty partial buffer | 5 seconds |
| Metadata bulk operations in flight per catalogue | 1 |
| Total buffered/in-flight encoded metadata payload | 64 MiB |
| Queue/checkpoint interval | About 30 seconds |
| Checkpoint operations in flight | At most 1 |
| Individual checkpoint chunk target | At most 8 MiB |
| Graceful-stop drain/flush budget | 5 seconds |

The 16 MiB **batch** ceiling is an application memory policy, separate from the 16 MiB **document** limit. The driver can split a bulk into further wire batches. Count operation-envelope overhead and driver/decoded-object copies when selecting real memory limits; the encoded-byte budget does not bound total process RAM by itself.

Flush a target buffer when it reaches 1,000 records, would cross the byte ceiling, reaches five seconds of age, needs to release global memory pressure, or the metadata phase is ending. A valid large product can be sent on its own. Never require 1,000 near-limit products to accumulate. Apply backpressure to completed-response delivery when the total memory budget is reached.

Use per-collection buffers and one metadata write coordinator per catalogue. HTTP requests and parsing remain concurrent; the coordinator can send one batch while bounded buffers accumulate subsequent captures. There is no global catalogue guard update, queue claim or run-counter write for each product or bulk.

### Write operations and idempotency

Use `InsertMany` or `BulkWrite` without starting an explicit transaction. Prefer independent upserts with stable identities when replay is expected. Deduplicate product identities within a bulk. Use unordered execution for independent records; do not put dependent writes for the same product into an unordered batch.

For a normal incremental capture, an insert-if-absent upsert such as `$setOnInsert` can retain the existing complete capture when a response is replayed. A duplicate identity is a reconciliation case, not evidence of a new successfully inserted product. A zero modification count can mean that the desired data is already present. If an existing legacy record is only an incomplete/failure stub, an insert-if-absent operation alone is insufficient: conditionally replace or repair it only while it still has that incomplete state/revision. Reconcile a conflict rather than overwriting a concurrently saved complete capture.

For any mode that deliberately replaces a saved capture, use a single-document conditional update against the expected capture revision. Retrying a prepared operation retains its capture revision and payload. If another capture has replaced it, reload/reconcile instead of overwriting blindly. A replay must not reset an already stored image to pending for the same capture. Stable identity alone does not prevent an older retry from overwriting newer data.

Do not read back every successful write. Reconcile only ambiguous/conflicting outcomes, using bounded grouped identity/revision reads where possible. Use driver-supported retries where available, but make application replay safe on a standalone server as well; correctness must not depend on replica-set retryable-write support.

### Partial success and uncertainty

A bulk is **not all-or-nothing**:

- Retain and count confirmed successful records even if other items fail.
- Remove confirmed successes from the live pending buffer; never roll them back because a neighbouring record failed.
- Isolate deterministic per-record errors so they cannot poison an endlessly retried batch.
- Keep transient or unknown outcomes unresolved while the process is alive. Back off, reconcile by stable identity/revision, and retry safely; pause admissions when storage cannot make progress.
- After a network/acknowledgement failure, do not assume every unreported item failed or every submitted item succeeded.
- Do not mark a bulk “catalogued” merely because it was queued, serialized, sent, or returned an aggregate count whose per-item outcome is still uncertain.

No separate queue-status update is required before returning a successful product result to the scheduler. The saved product is the evidence of capture.

## 7. Best-effort checkpoints and restart

A queue checkpoint is a **recovery hint**, not a transactional snapshot and not an authoritative completion ledger. Periodically coalesce current phase, selected targets, crawl mode, compact pending/active identifiers, retry due times, known cooldowns and recent small failure outcomes.

Default to about one checkpoint opportunity every 30 seconds, with at most one operation in flight. Skip/coalesce redundant checkpoint requests rather than stacking them behind metadata bulks. Attempt an additional checkpoint at phase boundaries and graceful stop if the time budget allows. Missing a checkpoint must not block each product save.

Store only compact identities/scheduling information, not buffered JSON or image bytes. Chunk large snapshots well below the document limit, label them with run/snapshot IDs, and identify which chunks belong together. A checkpoint may be incomplete; do not combine incompatible snapshots or treat a missing chunk as an empty queue. An implementation can publish a small manifest after writing its chunks, using ordinary independent writes; no multi-document atomic snapshot is required.

On restart:

1. Read the last usable checkpoint if one exists. Treat formerly active work as pending, not permanently claimed.
2. Discard unusable portions and perform fresh sitemap discovery when metadata work was interrupted or completeness is uncertain. Merge/deduplicate checkpoint hints with discovery.
3. Compare discovered IDs with saved complete products. A persisted product is retained even when its queue/checkpoint state lagged behind.
4. Refetch products whose payload existed only in memory. No original capture-intent recovery or JSON archive is available or required.
5. Derive remaining image work from the saved product's image state and capture revision.
6. Restore known future due times/cooldowns and re-establish robots policies before new requests.

Do not advance a resume cursor in a way that permanently skips products whose payloads were never acknowledged. Checkpoint “done” flags alone cannot justify skipping discovery. A checkpoint may record the metadata phase as finished only after discovery/workers drained and every remaining metadata bulk outcome was resolved; if that phase marker is absent or uncertain, rediscover.

A crash may lose more than one physical bulk: captures being built, buffered records for multiple targets, in-flight unknown results, recent failure diagnostics and uncheckpointed discovery/queue changes are all volatile. This is bounded during execution but is not an exact “at most 1,000 lost records” promise. Repeated discovery/fetching and approximate resumed counters are accepted.

Known server cooldowns still apply. A new Retry-After/429 cooldown should trigger a small coalesced target-cooldown write promptly, independently of product batches; this is rare control-state persistence, not a per-job ledger. Honour all available saved future cooldowns after restart. Exact preservation of an uncheckpointed cooldown across abrupt death is not promised.

## 8. Document phase followed by image phase

The default crawl has two phases across the **selected targets**.

### Phase A: documents

Discover and fetch product responses, validate in memory, and submit metadata bulks. No primary-image downloads or S3 uploads run in this phase. A validated product's saved image state is pending or not-available; creating that state requires only its product write.

End Phase A when discovery is exhausted or explicitly failed with an issue, all metadata workers/retries have reached terminal outcomes, and all prepared buffers and unknown bulk outcomes have been flushed/reconciled. Ordinary terminal product failures do not block the phase forever. An authentication/policy/storage pause is not successful phase completion.

### Phase B: images

Read saved products for the selected targets in bounded keyset pages, projecting only identity, capture/image revision, primary URL and existing image information. Include pending images and previously failed images eligible for a new run. Do not refetch successful product JSON just to retry its cover.

Queue those candidates in memory and process them with bounded image-download/upload concurrency. Update product image fields in small bulk groups, using the same 1,000-operation/16 MiB/five-second limits where applicable. No durable image claim or upload-intent document is required. In-memory deduplication prevents multiple active image tasks for the same capture within a run.

Retry transient image errors within a bounded per-run budget. Terminal image failures remain eligible in a later run; they do not invalidate the saved product. A normal crawl ends when eligible image work has finished or reached a terminal issue outcome and pending image-result updates have been resolved. Pending/retrying work keeps the image phase active; a storage outage pauses it.

### Lifecycle pseudocode

~~~text
function crawl(saved_config, selected_targets, mode):
    validate both saved storage configurations and required access
    initialise reused database client, memory queues and bounded buffers
    acquire optional advisory active-run lease
    restore usable recovery hints and known cooldowns
    establish target policies and cookies

    phase = documents
    discover and fetch metadata concurrently under queue/memory bounds
    for each validated capture:
        prepare complete product with stable identity and capture revision
        set initial image state in that same product document
        submit to collection buffer
    during this phase:
        flush on record/byte/age/memory limits
        reconcile partial and unknown bulk results
        coalesce occasional checkpoints
    drain metadata workers and resolve the final partial bulks
    if paused or stopped: use bounded stop/recovery rules; return
    best_effort_checkpoint(metadata_finished = true)

    phase = images
    scan saved pending/eligible-failed image states in bounded pages
    download and upload images with bounded independent workers
    buffer conditional product-image updates
    flush and reconcile final image results
    best_effort_checkpoint(phase = complete, summary)
    release optional advisory lease
~~~

A saved metadata-finished checkpoint can allow restart directly into Phase B. If the checkpoint is missing or suspect, replaying Phase A discovery is acceptable because saved complete products are skipped. Phase B can always reconstruct its work from products without a complete saved image queue.

## 9. Primary cover images and S3

Select one primary cover URL from each accepted response. The historical DriveThruRPG adapter prefers `webImage` and then `image`, resolving relative paths against `https://d1vzi28wh99zvq.cloudfront.net/`. Each other storefront adapter must verify its own URL/origin rules; shared platform branding does not establish identical endpoints.

Retain other image/preview/file references in the complete source response, but do not download galleries, previews or product files as part of this requirement. Download original primary-image bytes without mandatory resizing or conversion. Keep existing bounded size/timeouts, image content validation, approved public HTTP(S) origins, redirect checks, cookie scope, robots policy and per-origin pacing. Never upload an HTML challenge as an image or forward storefront cookies to unrelated origins.

Use a safe product-scoped, content-addressed key such as:

~~~text
prefix/catalogue/target/product_id/cover/sha256
~~~

Keep keys within S3's limits and reject unusable configuration instead of truncating it. Use conditional creation, such as `If-None-Match: *`, to avoid overwriting an existing object. Verify successful storage using a supported checksum/size result or readback. If an existing key or uncertain result must be reconciled, verify it before reuse; ETag alone is not a general content hash. Distinguish missing objects, permission failures and temporary outages. See [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html).

After a verified upload, buffer a **single-document conditional update** matching product identity and the saved capture/image revision. Set the image state and reference together: bucket, endpoint/namespace where needed, object key, optional object version, SHA-256, byte size, media type and observation time. Do not replace the whole product or overwrite newer source/capture fields. A stale or deleted capture yields no matching update and is reconciled without forcing the old result onto newer data.

Image states are `pending`, `stored`, `not_available` and `failed`. Persist a bounded error with a failed image result where possible. Only an acknowledged update after verified upload means the image is stored in the catalogue. A zero modification count on replay is not itself a failure; distinguish already-saved identical state from a revision conflict.

There is **no MongoDB/S3 transaction and no durable upload-intent protocol**. If the process crashes after upload but before saving its reference, an unreferenced S3 object can remain and the product can still say pending. A later image phase may download/upload or verify/reuse it again. This extra work and orphan storage are accepted; automatic orphan deletion is not part of this task and must not remove referenced objects.

If an image-result update would exceed the product document limit, keep the complete product and previous image reference intact, record a compact image-metadata-size issue, and stop repeating that deterministic failure during the run. Preserve the failure hint in the next available checkpoint. A future run may rediscover it if the hint was lost.

## 10. Single active writer and advisory ownership

The supported operating model is **one active mutating crawl/reprocess operation per catalogue**. Enforce this within an application instance. Local and remote MongoDB hosting are both supported; multiple simultaneous distributed crawler owners are not a required feature.

An optional per-catalogue lease may discourage accidental overlap between instances. Acquire/renew/release it with occasional atomic single-document operations and server-time expiry checks. Renew on a coarse heartbeat; never touch the lease as part of each product, queue move, image result or bulk write.

If a live lease held by another instance is observed, do not start a second run. If ownership loss or renewal failure is observed during a run, pause new admissions and new writes. Already submitted operations can still finish.

**The old strict fencing guarantee is removed.** A lease check followed by separate writes cannot guarantee that no delayed write lands after takeover. Capture-revision filters protect particular product updates; they are not catalogue-wide stale-owner exclusion and cannot prevent every stale insert into a previously absent product.

Treat checkpoints/run summaries as best effort under overlap, retain stable unique product identities, and make ordinary replay safe. Do not reintroduce a shared ownership write in every product operation to emulate the old transaction protocol. Strong multi-owner coordination would be a separate future requirement.

## 11. Pause, stop, reprocessing and visible progress

### Stop and interruption

On Stop, stop admitting new requests immediately, allow up to five seconds for active work and acknowledged flushes/checkpoints, then cancel remaining operations. Flush validated captures/image results if the deadline and storage availability permit. Do not promise successful flushing under an outage or force-kill. Never start the image phase merely to drain a metadata Stop request.

Abrupt death may lose volatile buffers and queue state. Existing acknowledged product documents remain the recovery basis. In-flight writes may have partially completed; the next run reconciles via stable identities. No durable return-to-pending operation is required for every abandoned task.

### Reprocessing

Read the stored MongoDB source response in bounded pages and recompute requested parser-derived fields in memory. Preserve original observation times and safe source metadata. Use bounded conditional single-document bulk updates; preserve previous complete records if validation/encoding/size checks fail.

Reprocessing makes no storefront requests and no S3 uploads. Whenever it adds, changes or removes the primary-image candidate, atomically assign a new image revision and set the state to pending when a candidate exists or not-available when it does not, preserving the original capture observation time. Retain that new image revision across retries. Image completion filters must match it as well as the capture revision, so an old download cannot attach the previous candidate's bytes after reprocessing. It uses the same saved-storage configuration gate and best-effort checkpoint/replay rules, with no archive or save-intent recovery.

### Dashboard semantics

- **Queued** counts discovered products still awaiting confirmed metadata persistence, including active, retrying, buffered and unresolved writes. Terminal failed products appear in Issues.
- **Catalogued** counts confirmed complete product records in the chosen dashboard scope. Buffering a product or replaying an already saved identity must not increase it.
- Counts can jump by a batch. Update the UI from in-memory counters at a modest interval; do not run aggregate/count queries or increment a shared MongoDB counter for every product.
- Show whether the current phase is document capture or image downloads. Images run after documents, with image activity/issues in the existing activity area; no additional storefront-count column is required.
- Image failure does not subtract a valid product from Catalogued. Queued can be zero while image work keeps the crawl active.
- Resumed queue/run-progress estimates may change after reconciliation because checkpoints are stale. Do not present a drained queue or completed run as proof of exhaustive storefront coverage.

The precise total-catalogue versus current-run scope of the existing Catalogued display remains a UI choice; whichever is used must distinguish already stored products from newly acknowledged captures and avoid double counting retries.

## 12. Retained crawler behaviour

Keep the following behaviour from the [historical reference](reference/KaijuCrawl_Rust_Crawler_Reference.md), while applying this document's storage, scheduling and recovery rules:

| Area | Retained behaviour | Historical reference |
| --- | --- | --- |
| Target identity | Positive ASCII decimal product IDs normalized as strings; target-scoped uniqueness, approved URLs and canonical API requests; no guessed ID enumeration or recommendation crawling | Section 2 |
| Sitemap parsing | Bounded XML/gzip parsing, direct loc/lastmod children, cycle protection, approved origins and no extension image:loc discovery | Section 6 parsing prose only |
| HTTP and robots | Per-origin policy/pacing, target cooldowns, bounded redirects/body sizes/timeouts and safe metadata | Section 5, with checkpoint persistence changes from section 7 above |
| Product acceptance | Exactly one direct matching candidate, full nonempty title/description, original JSON information, explicit gate detection and no nested recommendation substitute | Section 7 pure parse_and_validate and HTML cleanup |
| Response failures | Product-only ordinary 403, explicit authentication/challenge pause, bounded transient retries and terminal malformed/missing/restricted outcomes | Section 8, with in-memory scheduling and current storage errors |
| Cookies | Manual paste and Firefox import, correct origin/domain/path/expiry/container scope, protected secrets and non-mutating diagnostics | Section 9 |

Do not import historical fenced discovery pseudocode, archive publication, durable claims or per-attempt storage while reusing the parsers. The full historical extracted-field schema is optional; preserving complete source JSON does not require duplicating every derived field.

Keep the existing five-attempt transient retry budget during an uninterrupted run and approximately 10/20/40/80-second backoff plus bounded jitter; a longer valid Retry-After takes precedence. Identity, completeness and deterministic size failures do not use transient retries. Pause/resume within the same process preserves memory; abrupt restart has the checkpoint limitations already stated.

HTTP response ceilings remain 1 MiB for robots, 16 MiB for products and 128 MiB for sitemaps, with both transferred/decoded bounds where applicable. Parser-work limits and cancellation remain bounded; processing in memory does not authorize an unbounded parse or dropping existing response checks.

The inspected Rust implementation validates only DriveThruRPG's adapter. Verify the other three storefronts' API parameters, origins, primary images and authentication behaviour separately. The requirement for four targets does not invent compatibility evidence.

Read-only SQLite transactions used to obtain a consistent Firefox cookie snapshot are outside the removed MongoDB persistence transaction policy. Browser files must remain unmodified.

## 13. Acceptance and performance scenarios

These are implementation acceptance requirements, not claims that a running crawler or benchmark was executed while editing this specification.

1. **Standalone and remote-capable setup:** A standalone MongoDB instance passes storage preflight without transaction support. The same application can use a configured remote server. MongoDB and S3 settings remain mandatory.
2. **No transaction or per-job write path:** Metadata ingestion issues no explicit multi-document transaction, durable queue claim, per-attempt record, save/upload intent, ownership-fence write or shared counter update per product.
3. **Bulk limits:** Small valid products accumulate toward 1,000 records; byte/age/global-memory bounds and phase end flush smaller batches. A near-limit valid product is handled alone. Record the actual batch-size distribution.
4. **Acknowledged success:** Buffered/submitted records remain Queued until known saved. Confirmed identity matches on safe replay do not create duplicates or inflate counts.
5. **Partial bulk error:** A deterministic failed item does not discard neighbouring successes. Transient and ambiguous outcomes are reconciled/replayed safely; a timeout is not interpreted as proof of zero writes.
6. **Loss before flush:** Kill the process with unflushed captures and uncheckpointed queue changes. Losing that work is accepted. Fresh discovery/replay finds still-discoverable missing products and keeps already saved ones; it cannot recover an unsaved response that the source no longer provides.
7. **Stale/partial checkpoint:** Missing chunks, outdated active flags or a done cursor ahead of saved products cannot permanently skip data. Checkpoints never contain raw product bodies and are not written per queue move.
8. **Size failure:** An oversized BSON product fails without truncation/spillover. A failed refresh preserves the previous successful payload/reference. Its small diagnostic can lag or be lost before checkpoint.
9. **Phase isolation:** While Phase A runs, no product-image download/upload runs. Phase B starts only after the final metadata bulk is resolved. Restart after metadata capture reconstructs pending images from products.
10. **Image retry and crash:** A missing/failed image preserves the product. Killing the process after upload but before reference persistence can leave an orphan and pending image; later replay is safe and no upload intent is required.
11. **Stale image completion:** Updating with an old capture/image revision cannot attach the old result to a newer capture. Content-addressed objects do not overwrite unrelated/newer bytes.
12. **Advisory ownership:** An observed active owner prevents ordinary duplicate start, and observed loss pauses admissions. Tests must not claim the old strict no-stale-write-after-takeover guarantee.
13. **Stop and reprocessing:** Graceful stop attempts bounded flush/checkpoint; forced exit can lose buffers. Reprocessing uses saved MongoDB JSON without storefront/image fetches or S3 uploads, preserving old valid data on failure. MongoDB access and configured-storage preflight may still use the network.
14. **Crawl semantics:** Identity, response validation, cookie scope, robots, 403/authentication distinctions and bounded retries remain intact. Repeat discovery cannot duplicate product identity.
15. **Measured throughput:** Replay identical captured inputs into the real bulk persistence path, separating metadata and image phases. Measure acknowledged records/second, bytes/second, actual bulk count/size, command count per product, writer wait time and memory. Include partial failures and pre-existing products in a realistic collection; do not claim performance from enqueue speed alone.

Performance tuning should first reduce command count, serialization, unnecessary lookups and competing control-state writes. Do not reintroduce transactions or weaken saved-data acknowledgement merely to improve a counter. No particular records/second result is guaranteed without a representative workload measurement.

## 14. Decisions deliberately left outside this change

- Full-recrawl refresh/overwrite and history-retention semantics remain open. This update does not authorize replaying an old result over a newer record; any future replacement policy must use the single-document revision rules above.
- Exact normalized catalogue schema and advanced search remain open; complete source retention is already required.
- Four storefront adapters must be verified individually; the historical source only supplies DriveThruRPG evidence.
- Migration from existing KaijuCrawl SQLite/archives or an earlier MongoDB job/intent schema is separate. Old operational collections must not remain authoritative accidentally; retain saved product data and require an explicit migration plan for destructive cleanup.
- Strong distributed multi-owner coordination and automatic S3 orphan cleanup are separate future features.

The [historical reference](reference/KaijuCrawl_Rust_Crawler_Reference.md) remains available for source details and evidence. Its old storage, restart and ownership conformance scenarios are not current implementation gates.

