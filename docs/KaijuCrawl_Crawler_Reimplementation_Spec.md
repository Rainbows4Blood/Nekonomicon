# KaijuCrawl: crawler specification and pseudocode

**Source:** [`Rainbows4Blood/kaijucrawl`](https://github.com/Rainbows4Blood/kaijucrawl) at commit [`f2ced97ac62f24e32abd51fd10b88fb7bda344af`](https://github.com/Rainbows4Blood/kaijucrawl/commit/f2ced97ac62f24e32abd51fd10b88fb7bda344af), the `main` head inspected on 31 October 2026.

**Purpose:** Extract the behavior of the Rust crawler so another engineer or agent can build a lean reimplementation. This is an **as-implemented** specification, not a request to reproduce the Tauri UI, catalogue browsing, AI analysis, or the rest of the application. The source and its synthetic tests are the evidence; no live storefront crawl was performed for this document. Endpoint behavior can change independently of the repository.

**Reading convention:** “Current behavior” describes the inspected code. “Lean implementation choice” is an architectural option, not a claim that the original makes that choice. A simplification that removes an observable feature is called out explicitly.

> **Nekonomicon implementation update:** The storage requirements immediately below are authoritative for the new application. KaijuCrawl's SQLite database, local archive directory and OS-file-lock ownership are historical reference behavior; do not implement them as Nekonomicon's storage design. Sections 1–13 retain the original source observations and pseudocode for provenance. Apply the MongoDB/S3 substitutions below wherever those sections describe persistence, recovery, reprocessing or ownership. This change does not establish support for an existing KaijuCrawl database, or decide the still-open semantics of a full recrawl.

## Nekonomicon storage requirements

### Storage responsibilities

Nekonomicon uses **MongoDB for durable catalogue and crawler state** and **Amazon S3 for immutable source-response archives**. Both are mandatory, including for a local desktop installation. A custom S3-compatible endpoint may be configured; it must provide the conditional-write and read semantics this protocol needs. Do not silently fall back to SQLite or local files if either service is missing or unavailable.

| Data | Authoritative location |
| --- | --- |
| Normalized product records, discovery sightings and provenance | MongoDB |
| Crawl runs, sitemap/product jobs, attempt outcomes, retries and cooldowns | MongoDB |
| Catalogue identity, writer ownership/generation, schema versions, archive references and pending save intents | MongoDB |
| Validated, gzip-compressed source response bodies | S3 bucket and configured prefix |
| Connection settings | Local application configuration; secrets in the OS credential store or another protected secret provider |

MongoDB documents have a 16 MiB BSON limit. The source permits a decoded response of up to 16 MiB and can preserve both the accepted raw object and its outer envelope. Do not truncate that data or silently reduce the response limit just to fit it into one document. Keep the original complete response in S3 and expose the raw metadata through its archive reference; split or spill oversized derived fields as needed while preserving their complete values and consumer access. Persist exact decimal text and source number precision as described in the historical metadata contract.

Store crawled product records in **one MongoDB collection per crawl target**. The initial targets—DriveThruRPG, DMs Guild, Storytellers Vault and Ulisses ebooks—therefore each have their own product collection; future crawl targets get their own collections as well. Product identity remains scoped to its catalogue, target and normalized product ID. Do not automatically merge records across target collections. Shared collections may hold operational state such as runs, jobs, ownership, discovery and save intents; their records remain target-scoped where relevant. Exact collection names and the product document schema are separate implementation decisions.

Within each target's product collection, enforce a stable unique index on `(catalogue, normalized product ID)`. Also enforce stable unique indexes for product jobs, sitemap jobs `(run, target, URL)`, discovery sightings and save intents. Queue claims, retries and updates must be atomic and durable across restarts. Use a transaction-capable MongoDB deployment (a replica set or sharded cluster), including a local replica set if self-hosted. Use short multi-document transactions where changes to jobs, products, intents and ownership must commit together, with majority-acknowledged writes. See the [MongoDB transaction documentation](https://www.mongodb.com/docs/manual/core/transactions/).

### Configuration and crawl gate

Provide a **Storage** settings screen for:

- **MongoDB:** connection string and database name. A connection string can include credentials; protect and redact it accordingly.
- **S3:** bucket, region, optional object-key prefix and optional custom endpoint. Support the AWS default credential provider chain or explicitly entered access key and secret; a temporary session token is optional. Never place credentials in URLs, logs or diagnostic messages.

Save non-secret settings persistently and secrets in the platform credential store or a protected provider. Do not put secrets into MongoDB records, S3 object keys, application logs, mockup preview URLs or exported diagnostics. Show whether each service is **configured**, **not configured**, or has an actionable error; a saved configuration is not a claim that a connection has succeeded. Offer a **Test connection** action for each service with clear success/failure feedback. Testing is optional before starting a crawl; never mistake a syntactically complete form or a successful ping for proof of all required permissions. Connection tests must not mutate product, job or archive data. A short-lived, uniquely named S3 test object may be written, read and cleaned up under a dedicated test prefix, with a clear error if cleanup fails.

The **Start crawl** action stays disabled until a complete, syntactically valid MongoDB configuration and a complete, syntactically valid S3 configuration have both been saved, and at least one storefront is selected. Explain which service is missing and link directly to Storage settings. Apply the same rule to regular and full crawls. The backend must enforce it too, including for direct commands and resume; UI state alone is not an authorization or data-integrity boundary. At start/resume, resolve credentials, establish MongoDB connectivity and transaction support, and verify the configured S3 destination and required access. If preflight fails, report the affected service and start no storefront requests or crawl jobs. A prior test does not replace this preflight.

If a service becomes unavailable during a run, stop admitting new work and pause safely; retain durable claims/intents for recovery rather than reporting products as successful. Report which service needs attention. Resume only after the configured services and ownership are restored and pending intents have been reconciled. Apply the same storage requirements to reprocessing archived responses, which reads MongoDB and S3 but makes no storefront requests.

A running operation uses a stable destination snapshot; do not switch its database, bucket, endpoint or prefix mid-run. Bind the catalogue to its archive namespace, store enough non-secret location information with archive references to identify it, and never silently reinterpret old references after a settings change. Changing destinations for existing data requires an explicit migration or a new catalogue; implementing migration is a separate requirement.

### Durable capture and recovery across MongoDB and S3

MongoDB and S3 do not share a transaction. Preserve the original completeness, no-overwrite and recovery guarantees using an idempotent save-intent protocol:

```text
function save_validated_capture(product, decoded_response):
    require complete product and expected target/product identity
    digest = SHA256(decoded_response)
    key = prefix / catalogue_uuid / target / product_id / (digest + ".json.gz")
    bytes = gzip(decoded_response)
    transaction(fenced): insert or recover save intent; job.state = persistence
    conditionally put object at key without replacing an existing object
    if object already exists or the put outcome is uncertain:
        read the existing object; never assume an ETag is the body hash
    read object back; decompress and verify decoded length and SHA-256
    transaction(fenced):
        insert complete product if absent; record verified archive reference
        finalize intent; mark job successful
```

Use an S3 conditional create such as `If-None-Match: *`; handle an existing key or concurrent conflict by verifying the existing object or retrying safely. Do not classify a permission error or timeout as a missing object. Never overwrite a successful archive. Record the actual stored object's lengths, decoded hash and, if applicable, version ID. Do not hold a MongoDB transaction open during an S3 operation. Store only validated successful responses. S3 upload failures or failed verification must not produce a successful product.

On restart or resume, scan unfinished intents in bounded batches. If the referenced object exists, read, decompress, hash and reparse it before a fenced finalization. If it is definitely absent, requeue the product; if it is corrupt, inaccessible or its state is uncertain, pause and report the storage problem. A crash after uploading but before committing MongoDB state can leave an orphan object; cleanup must be conservative and never delete an object referenced by a product or pending intent. Reprocessing reads and verifies the original S3 object, makes no storefront request and preserves the original observation times. See [S3 conditional writes](https://docs.aws.amazon.com/AmazonS3/latest/userguide/conditional-writes.html).

### Shared-catalogue ownership

A local OS file lock cannot protect a remotely shared catalogue. Use a MongoDB-backed, per-catalogue renewable lease with an owner ID, expiry and monotonically increasing fencing generation, evaluated against server time. Lease acquisition and takeover must be atomic. Each authoritative mutation must be fenced by the current owner and generation in the same transaction; write/guard the same ownership document so takeover and a stale writer cannot both commit. If the lease cannot be renewed, stop new work and reject further writes until ownership is reacquired and recovery completes. Do not delete or reset the generation on expiry. Lease expiry permits recovery after process death; it is not instantaneous like the original OS lock. Conditional immutable S3 writes and intent recovery handle any upload completed after a lease loss.

### Storage acceptance scenarios

1. With neither service saved, with only MongoDB, or with only S3, both regular and full crawl starts are blocked in the UI and backend. No crawl run or storefront request is created. Both configured plus a selected storefront enables the start control; a failed preflight still prevents the run.
2. Configuration survives application restart, secret values are protected and redacted, and a failed connection test does not erase the previous saved configuration. AWS provider-chain credentials and explicit credentials are both supported.
3. MongoDB/S3 interruption during discovery, upload or finalization stops new work. After service restoration and lease reacquisition, unfinished work is reconciled without lost products, duplicate jobs or a false successful state.
4. Crash before/after intent creation, during/after S3 upload and before/after the MongoDB final commit. Recovery handles missing, existing, corrupt and uncertain object outcomes without overwriting a successful archive. A stale owner cannot finalize after takeover.
5. A response near the accepted 16 MiB limit remains fully recoverable from S3 and the complete extracted metadata remains accessible despite MongoDB's document-size limit.
6. Reprocessing uses the verified original archive and MongoDB only; it does not fetch the storefront or change original capture times. Switching a configured bucket or database never silently redirects existing archive references.

## Historical KaijuCrawl behavior and pseudocode

The following numbered sections document the source implementation, not Nekonomicon's chosen storage backend. Their SQLite, local-filesystem, OS-lock and related recovery instructions are historical; use the MongoDB/S3 requirements above for Nekonomicon.

## 1. What the crawler does

1. Discover DriveThruRPG product URLs by following the root sitemap, nested sitemaps, and accepted sitemap declarations from robots policies.
2. Convert each recognized URL to a normalized positive decimal product ID, scoped to the target `drivethru`.
3. Retrieve product details from a fixed JSON API endpoint, subject to robots rules, target cooldowns, bounded concurrency and authenticated cookies when supplied.
4. Verify that the response directly and unambiguously describes the requested ID. Require a nonempty title and a nonempty **full** description after HTML cleanup.
5. Extract the metadata, archive the validated response body, and commit the product record and queue outcome durably.
6. Retry transient errors within a finite per-run budget. On a later run, retry unsuccessful products and rescan sitemaps; never refetch an already successful product during ordinary crawling, even when a sitemap `lastmod` changes.
7. Recover interrupted saves and support reprocessing archived successful responses after a parser-version change, without network requests or changing original observation times.

This makes a catalogue of **first successful observations**, not an automatically refreshed mirror. A completed run may still have failed jobs or undiscovered products.

**Target scope:** The inspected implementation has exactly one target: DriveThruRPG. DMsGuild, Storytellers Vault, Ulisses and other OneBookShelf storefronts are not implemented targets here. Do not infer their API parameters or support from the shared platform. Target-scoped identities and interfaces leave room for adapters later. See [target constants and URL rules](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs#L4).

## 2. Essential target constants and identity

| Item | Current value |
| --- | --- |
| Target key | `drivethru` |
| Website origin | `https://www.drivethrurpg.com/` |
| API origin | `https://api.drivethrurpg.com/` |
| Root sitemap | `https://www.drivethrurpg.com/sitemap.xml` |
| Canonical product URL | `https://www.drivethrurpg.com/en/product/{id}` |
| Product API | `https://api.drivethrurpg.com/api/vBeta/products/{id}?groupId=1&siteId=10` |
| Relative image base | `https://d1vzi28wh99zvq.cloudfront.net/` |
| Full-preview base | `https://watermark.drivethrurpg.com/` |
| Currency fallback | `USD`, labeled as a frontend-base-currency assumption |
| Request User-Agent | `Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:158.0) Gecko/20100101 Firefox/158.0` |
| Robots matching identity | `KaijuCrawl/{package_version}`; the matcher uses its product token |

A product ID contains only ASCII decimal digits, must not represent zero, and is stored as a string with leading zeroes removed. Thus `00042` is `42`; `0`, `-2`, `2.1`, and `1e3` are invalid. Do not parse IDs into a fixed-width integer: the existing code accepts IDs larger than `u64`. The product key is `(target_key, normalized_product_id)`, not an ID alone.

For product discovery, accept HTTPS URLs on `drivethrurpg.com` or `www.drivethrurpg.com`, without credentials or a non-default port, in these forms:

- `/product/{id}` or `/product/{id}/...`;
- `/{locale}/product/{id}` or `/{locale}/product/{id}/...`, where the locale is at most five ASCII letters or hyphens;
- `/product_info.php?products_id={id}` when there is exactly one `products_id` parameter.

Query strings and fragments on discovered product aliases do not become product identity. Preserve the original alias for provenance and queue the canonical URL. Other paths and foreign, HTTP, credential-bearing or disallowed-port URLs are ignored. Approved nested sitemap URLs must be HTTPS website URLs ending in `.xml`, `.xml.gz`, or `/sitemap`, case-insensitively; their fragments are removed.

**Important compatibility distinction:** Product response requests always use the API URL above. Product-page HTML is not a fallback source. The crawler does not enumerate IDs or discover products from listings, categories, recommendations or related-product links.

Sources: [target adapter](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs#L28), [ID implementation](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/domain.rs#L43), [URL and identity tests](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/contracts.rs#L21).

## 3. Inputs, outputs and state

### Inputs

- An explicit **start crawl** or **reprocess saved responses** action. Starting the application alone does not start a crawl.
- For a crawl, an optional cookie source: anonymous, separately pasted website/API Cookie headers, or an imported browser profile. The desktop shell also requires the target to be selected.
- Local durable state, including prior products, jobs, run history, target cooldowns, pending save intents and archives.
- Storefront robots files, sitemaps and product API responses.

### Outputs

- One durable, complete product per `(target, id)` and one immutable successful source archive for its capture.
- Durable job states, attempt counts, failure reasons, discovery provenance and run summaries.
- Progress such as queued/active/retrying/failed products, discovery state, successful products, active work and cooldown. Do not present a drained queue as proof of full storefront coverage.
- A pause or failure with sanitized diagnostic information when authentication, policy, parsing or storage prevents progress.

### State model for a lean implementation

The existing SQLite schema is more extensive, but these concepts are the minimum needed to reproduce the crawler behavior:

| Entity | Key and relevant state |
| --- | --- |
| Catalogue | Stable catalogue UUID; one writer owner and monotonically increasing ownership generation. |
| Run | UUID, `crawl` or `reprocess`, status, start/end times, runtime snapshot and summary/checkpoint. |
| Target | Target key and persisted shared cooldown-until time. |
| Job | Kind (`sitemap` or `product`), target, identity, URL, run, state, due time, attempts in this run, lifetime attempts, claim owner/generation, outcome and timestamps. |
| Discovery | Product job + source sitemap + original alias; first/last seen, optional `lastmod`, most recent run. |
| Product | `(target, id)`, normalized record, parser/record versions, original fetch/check times, HTTP metadata and archive reference. |
| Save intent | Intent UUID, target/id, fetched-at time, API URL and safe HTTP metadata, archive path/hash/size, finalization state. |
| Attempt/reprocess outcome | Sanitized outcome and provenance; enough to inspect failures and recover without archiving failed bodies. |

Product jobs are unique by `(target, id)` across runs. Sitemap jobs are unique by `(run, target, sitemap URL)`, so cycles and duplicate links cannot produce infinite sitemap work in a run. Current states include `pending`, `active`, `retry`, `persistence`, `successful`, `done` for a completed sitemap, and `failed`. `successful` is a product's terminal state for normal crawling.

The implementation stores data under `<user-home>/.kaijucrawl/`, with SQLite authoritative for metadata and the queue, and gzip archives under `archives/`. Current catalogue database schema version is **16**; product record schema is **2** and parser version is `drivethru-rust-1`. A fresh lean implementation need not copy unrelated application tables or schema version 16. **Opening an existing KaijuCrawl catalogue in place is a separate compatibility requirement:** it requires the existing migrations, schema, archive references, ownership protocol and preservation of unrelated data, not merely the simplified model above.

Sources: [initial schema and indexes](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/migrations/002_local.sql), [runtime and versions](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/domain.rs#L6), [persistence operations](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L283).

## 4. Top-level crawl lifecycle

```text
function start_crawl(config, cookies):
    require target is selected and no other mutating operation is active
    acquire exclusive OS file lock for this catalogue
    open SQLite; migrate supported schemas under lock
    create fresh owner UUID and advance generation; fence all mutations
    validate archive storage and clean abandoned staging files

    run = create_run("crawl")
    mark unfinished older runs interrupted
    for every product job whose state != successful:
        assign it to run; set pending; reset per-run attempts and old outcome
        # Existing due time and target cooldown are not reset.
    enqueue the fixed root sitemap for this run

    recover_pending_save_intents()
    release abandoned active claims for this run
    restore target-wide persisted cooldown
    load required website and API robots policies
    enqueue accepted sitemap declarations from those policies

    while not stopped:
        if authentication/policy failure or storage/ownership failure:
            stop admitting requests; drain active work; report paused state
            wait for explicit resume or stop
            on resume: verify or reacquire ownership, validate storage,
                       replace cookies, recover intents, reload robots
            continue                       # same run, same attempt budgets

        flush validated captures when batch full, flush timer expires,
            no workers remain, or stopping
        claim due jobs up to remaining bounded worker capacity
        reserve at most one active sitemap job; use other capacity for products
        start claimed jobs concurrently
        collect completed results and persist their outcomes

        if no ready/active/retry/persistence work and no pending sitemaps:
            finish run                    # failed jobs may still exist
        else if only future-due work remains:
            wait until work is due or the run is interrupted

    on stop: stop new requests, allow active work a short grace period,
             cancel remaining work, flush completed valid captures,
             release active claims to pending and finalize run
    release ownership and the OS lock
```

This pseudocode combines `new_run`, `recover`, the scheduler and controller actions; exact event-loop structure is an implementation choice. The code inserts the new run and resets unfinished product jobs **before** intent recovery, but excludes products with unfinished intents from normal claims. Resuming a paused run is distinct from starting a new run: the same run's attempt budgets are preserved. A newly started run makes unsuccessful products eligible again but retains outstanding due times and server cooldowns.

The default implementation claims at most 20 jobs per batch, saves at most 10 validated captures per batch and flushes a partial save batch after 500 ms or when work drains. These are batching parameters, not added network delays. It uses at most one active sitemap job per run so website discovery cannot occupy every worker, while product work and discovery proceed concurrently. With one worker, the sitemap takes that slot when due. Only mark an attempt as started immediately before sending the first request, after waiting for policy, rate limits, cancellation and ownership. Claiming or waiting by itself must not consume an attempt; redirects within that request do not consume additional job attempts.

Sources: [scheduler job and run setup](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L107), [scheduler loop](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L406), [queue selection](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L357).

## 5. Robots policies, HTTP and pacing

### Policy setup

Fetch and establish `/robots.txt` independently for the `www.drivethrurpg.com` website origin and the `api.drivethrurpg.com` API origin before normal crawling. A 404 or 410 means no restrictions were declared for that origin. An unavailable, invalid, oversized or challenged policy is not silently treated as permission: authentication or policy failures pause setup. Robots fetches themselves use eligible cookies, the fixed request User-Agent, target limits, timeouts and redirect rules, but do not require a previously loaded robots policy.

For subsequent requests, check the policy for the exact request origin before each request, including redirects. A particular URL disallowed by robots yields a `robots` job failure; this differs from failing to establish the required policy, which pauses the target.

Parse accepted `Sitemap:` declarations as discovery roots. For pacing, select the most specifically matching robots user-agent group(s), take the greatest valid `crawl-delay` or `request-rate` interval, and keep independent request-start pacing per origin. One origin's robots interval must not impose a delay on the other origin; both still share the target's overall worker capacity. The request header remains the fixed Firefox string; robots decisions use the KaijuCrawl identity.

### HTTP requests

- Maintain a separate bounded HTTP limiter and shared server cooldown **per target**, even if future targets share an API host. There is no global host-wide limiter and no additional fixed request interval.
- Use HTTPS; allow only adapter-approved website/API origins. Recheck each redirect and cookies before sending. Normal requests allow up to eight redirects; robots requests allow up to six. Redirects must stay on the original origin. A product redirect must additionally preserve the exact `/api/vBeta/products/{id}` path and the two required query parameters `groupId=1`, `siteId=10`; it cannot switch products or site/group.
- Send `Accept-Encoding: gzip`; use `Accept: application/json` for product requests and `*/*` otherwise. Bound both transferred and decoded bytes; reject unsupported encodings and malformed gzip.
- Use 15 seconds connect timeout, 45 seconds read-inactivity timeout and 300 seconds total response timeout. Transport and read timeouts are transient job failures.
- Record only safe successful HTTP metadata: status and, when at most 1,024 characters, `ETag` and `Last-Modified`. Do not persist failure response bodies or cookies in logs, records or archives.
- `Retry-After` accepts nonnegative seconds or an HTTP date, is capped at one year, and extends a target-wide cooldown. HTTP 429 imposes at least 60 seconds. Persist the cooldown so a restart cannot bypass it.

| Resource | Encoded / decoded limit |
| --- | ---: |
| Robots | 1 MiB |
| Product response | 16 MiB |
| Sitemap | 128 MiB |
| Parser worker reply | 128 MiB |

Sources: [policy parsing and per-origin pacing](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs#L55), [robots loading](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs#L255), [request execution](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs#L357).

## 6. Sitemap discovery

A sitemap can be plain XML or a gzip file detected by magic bytes. It may also arrive with HTTP gzip content encoding. Stream the decoded XML and accept `sitemapindex` and `urlset` roots, including their own namespace prefix. For direct `sitemap` or `url` children, read only their direct `loc` and optional `lastmod`; ignore extension locations such as `image:loc`. Reject malformed XML, document types, missing locations, multiple roots, excessive depth or oversized input. The implementation bounds a location to 8,192 bytes, `lastmod` to 1,024 bytes, nesting to roughly 128 levels, and emits batches of at most 1,000 entries.

```text
function process_sitemap(job, response):
    for batch in stream_sitemap_entries(response.body, max_batch=1000):
        transaction with owner/generation fence:
            for entry in batch:
                if entry came from sitemapindex:
                    url = approved_sitemap_url(entry.loc)
                    if url exists:
                        insert sitemap job unique by (run, target, url)
                else if entry came from urlset:
                    id = extract_normalized_product_id(entry.loc)
                    if id exists:
                        upsert product job unique by (target, id)
                        preserve successful state on conflict
                        upsert sighting(source=job.url, alias=entry.loc,
                                        lastmod=entry.lastmod, run=run)
        commit transaction
    mark sitemap job done
```

A sitemap failure after earlier batches have committed can leave those discoveries present. Retrying is safe because jobs and sightings have uniqueness constraints. `lastmod` is a provenance hint, not a trigger to refresh a successful product. Each new crawl scans root and nested sitemaps again.

Source: [streaming sitemap parser](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/sitemap.rs#L19), [discovery persistence](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L321), [sitemap tests](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/contracts.rs#L325).

## 7. Product fetch, validation and extraction

### Main algorithm

```text
function process_product(job):
    response = bounded_get(product_api_url(job.id), product_id=job.id)
    classify HTTP, explicit login/restriction/challenge evidence
    if classified failure: record outcome; return

    product = parse_and_validate(response.body, job.id, response.final_url)
    if parsing, identity or completeness fails: record failure; return

    intent, compressed = prepare_immutable_archive(
        catalogue_id, target, job.id, response.decoded_body,
        fetched_at, response.final_url, safe_http_metadata)
    persist intent and move job to persistence state
    atomically publish archive and verify decoded size + SHA-256
    submit (intent, product) for a fenced database commit
```

```text
function parse_and_validate(body, expected_id, final_api_url):
    reject body above 16 MiB
    require final_api_url to match expected API origin, product ID,
        path, groupId=1 and siteId=10
    decode JSON with arbitrary-precision number preservation
    reject explicit root/accepted-object restrictions, authentication gates,
        API error envelopes and malformed JSON

    choose candidate(s) only from the direct payload, its direct `data`,
        or its direct `product` envelope; arrays are allowed at this level
    use `attributes` as the candidate product when present
    recognize direct ID fields productId, product_id, productID, id, @id, url
    normalize each valid ID; require exactly one matching direct candidate
    reject conflicting recognized IDs or an ID-only candidate
    never search nested recommendations for a substitute product

    optionally resolve publisher/authors/artists/editors/contributors through
        direct JSON:API relationships: require a unique included item with
        matching ID and, if provided, matching type
    title = clean_plain(first of description.name, name, title)
    full_description = first of description.description,
        string-valued description, fullDescription
    html, text = sanitize_and_extract(full_description)
    require title.trim() != "" and text.trim() != ""
    extract remaining metadata; preserve accepted source and outer envelope
    return complete product for (drivethru, expected_id)
```

A short description cannot stand in for the full description. Gate detection must be based on actual response flags or explicit page/notice evidence, not on words appearing in a book description, hidden dialogs, scripts or asset URLs. The parser rejects explicit `ageGate`, `ageGateRequired`, `requiresAgeVerification`, `adultContentRestricted`, `loginRequired`, `authenticationRequired` or `accessDenied` flags at relevant parsed levels. A valid JSON body with the wrong ID, duplicate matching candidates, conflicting IDs, no substantive product fields, or only a nested matching recommendation is not a successful capture.

### Worked example

A product response for requested ID `42` might contain:

```json
{
  "productId": "00042",
  "description": {"name": "Example", "description": "<p>Full text.</p>"},
  "price": "10.00",
  "specialPrice": "0",
  "onSale": true
}
```

The relevant normalized fields include `product_id = "42"`, `title = "Example"`, `description_text = "Full text."`, `base_price = "10.00"`, `price = "0"`, `currency = "USD"` and `currency_source = "frontend_base_currency"`. The complete record also contains the other contracted fields and raw metadata. The fallback currency is explicitly labeled; it is not inferred from the price.

### HTML cleanup

Sanitize full HTML and remove active/non-content elements including scripts, styles, templates, iframes, objects, embeds and noscript content. Produce both sanitized HTML and plain text. Preserve paragraph, heading, line-break, list and table boundaries; do not insert spaces inside inline text such as `to<b>get</b>her`. Collapse excess blank lines and normalize whitespace. A description that becomes empty after cleanup is incomplete.

### Metadata contract

For full behavioral parity, emit these fields, retaining unknown nested source data as described below. Missing optional values remain null or empty collections as appropriate; do not fabricate them.

| Group | Fields / extraction notes |
| --- | --- |
| Identity and provenance | `target_key`, `product_id`, `url`, `product_url` (canonical plus optional slug), `slug`, `source_kind=public_api`, `source_url`, `api_url`. |
| Descriptions | `title`, `description_html`, `description_text`, `short_description`. |
| Publisher and contributors | `publisher`, `publisher_id`, full `publisher_metadata`; deduplicated `authors`, `artists`, `editors`, `contributors`, and role/name `credits`. |
| Commercial | `price`, `base_price`, `special_price`, `on_sale`, `currency`, `currency_source`, `lowest_prices` (digital, print, any), `formats`, `offers` with their original raw values. If `onSale` is true and special price is valid, it is the effective `price`; otherwise use base price. Zero is valid. |
| Bibliographic | `page_count`, `isbn`, `sku`, `edition`; integer values must really be integral and fit the chosen stored integer type. |
| Dates | A `dates` object retaining source keys beginning `date`, plus `fileLastModified` and `publicationDate`; normalized `date_created`, `date_modified`, `date_available`, `file_last_modified`. |
| Taxonomy | `filters`, `primary_filters`, `categories`, `filter_groups`, `genres`, `themes`, `rule_systems`, `product_types`, `languages`, and joined `language`. Each taxonomy object retains its raw source, IDs, parent/ancestor IDs, slug and localized descriptions. |
| Reviews and files | `rating`, `rating_count`, `review_count`, `review_ratings`, `reviews`, `ranking`, `files`, `file_size_reported`. If direct rating count is absent, sum all five nonnegative buckets only when all exist and the sum fits; a zero rating with zero reviews becomes unknown. |
| Media and links | `cover_image`, `images`, `previews`, `description_links`. Relative image URLs use the image base; `previewFullFile` uses the watermark base; other preview links use the website base. Only HTTP(S) URLs without embedded credentials survive URL resolution. Media is referenced, not downloaded. |
| Relations and flags | `related_products`, `is_bundle`, `bundle_id`, and present status/format/content flags. Related IDs do not themselves create crawl jobs. |
| Version and raw data | `parser_version`, `record_schema_version`, `extraction_status=complete`, `missing_fields=[]`, `raw_metadata.public_api`; when the accepted object differs from the outer response, also keep `raw_metadata.api_response`. |
| Capture metadata | `fetched_at`, `last_checked_at`, `reparsed_at`, safe `http`, `archive` and `raw_path`; discovery and crawl status are derived from job/sighting state in the existing catalogue view. |

Prices and ratings are preserved as exact decimal text; do not convert through binary floating point. JSON number parsing must retain source precision, including nested raw numbers. Explicit currency from the response is labeled `api_response`; absent currency becomes `USD` with source `frontend_base_currency`—this is an assumption, not a currency conversion.

For filter grouping, ancestor or parent ID `10` means genre, `20` product type, `30` rule system, `40` language, and `44498` format. Prefer English taxonomy descriptions (`languageCode=en` or `languageId=1`), otherwise the first description; preserve all localized labels and raw objects. Merge grouped values with applicable direct fields and deduplicate while preserving order. The [synthetic full-product fixture](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/fixtures/full_product.json) and [metadata assertions](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/contracts.rs#L203) are useful examples.

The current parser executes in a separate child process without credentials. It has a 30-second deadline **after a parser slot is acquired**, bounded input/output, and on timeout/cancellation kills and reaps the child. Parser and download capacity each default to the available logical CPU count, or one if unavailable. This isolation is part of the current hard-timeout behavior; replacing it with an in-process parser is a valid lean design only if its inability to forcibly terminate a stuck parse is accepted as a behavior change.

Sources: [identity and envelope parsing](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/parser/extract.rs#L158), [field extraction](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/parser/extract.rs#L289), [isolated worker](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/parser/runtime.rs#L87).

## 8. Failure classification, retries and pause behavior

| Observation | Current outcome |
| --- | --- |
| Transport failure, timeout, HTTP 408, 425, 429 or 5xx | `transient`; retry within the current run when attempts remain. |
| HTTP 404/410 | `missing`; fail this job for this run. Robots 404/410 instead establish an empty policy. |
| Ordinary product API HTTP 403, including a generic `access denied` message | `restricted`; fail this product for this run, do not pause other products, and retry on the next run. |
| Product API HTTP 403 with an explicit login/session flag or recognized message; `cf-mitigated: challenge`; other recognized login/challenge evidence | `authentication`; pause the target. A 200 challenge is still a failure. |
| Explicit age/product restriction | `restricted`; fail the product for this run. |
| Failed required robots policy, unapproved redirect, or inability to establish policy | Policy/authentication pause as classified. A specific URL excluded by an established policy is a `robots` job failure. |
| Malformed/oversized response, unsupported encoding, ambiguous/wrong identity, schema error, incomplete title/description, parse timeout | Fail that job for this run; do not archive its response. |
| SQLite, archive, ownership or other local persistence failure | Stop new admissions and pause the crawl for recovery. |

The general classifier checks explicit JSON gate flags before ordinary status handling; an ordinary HTTP 401 is classified as authentication. For HTML, the gate recognizer looks for explicit visible title/heading/short notice or a specific challenge form, and ignores hidden content, scripts, styles, templates and noscript. This avoids treating ordinary product text or security scripts as an access denial.

Each job gets at most **five actual attempts per run**. Only transient failures retry immediately in the same run. For retry after attempt `n` (where the first completed attempt is `n=1`), compute:

```text
base = min(10 seconds * 2^(n - 1), 3600 seconds)
jitter = job_numeric_id mod 3 seconds
job_due_delay = max(valid Retry-After delay, base + jitter)
```

A longer Retry-After also extends the persisted target cooldown shared by discovery, API calls and retries. The first four retry delays in a default five-attempt run start around 10, 20, 40 and 80 seconds, respectively. Once the budget is exhausted, mark the job failed for this run. The next new run resets the attempt count and retries unsuccessful products, subject to retained due times and cooldowns. Authentication/policy failures return the job to pending and pause; explicit resume continues the same run without resetting its attempt count.

Sources: [status and product-403 classifiers](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs#L107), [explicit gate detection](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru/access.rs), [retry and outcome storage](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L420), [403 synthetic tests](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http/tests.rs#L318).

## 9. Authentication and diagnostic boundary

The HTTP engine can work with no cookies. Manual input is a single-line `name=value; ...` Cookie header, optionally prefixed with `Cookie:`. Reject control characters, malformed cookie names, extra header injection and inputs over 1 MiB. Preserve valid browser-style values and their ordering. The separately pasted website and API headers are tied to their **exact origins**; they are not forwarded to another host.

Browser import reads only the selected local profile and relevant DriveThruRPG domains, using a read-only SQLite transaction so WAL contents are included. Never modify browser files or terminate the browser. Imported cookies retain host-only/domain, path, secure, expiry and supported first-party partition scope. When constructing a header, filter by the actual request URL and time, then sort longer paths first and older known creation times first. Do not flatten a cookie's scope just because it was imported.

Firefox import also supports a selected container, schema-specific expiry units and eligible cookies from the newest of the current `recovery.jsonlz4` and `sessionstore.jsonlz4` snapshots. It never falls back to old/upgrade backups. Database values win over duplicate session entries. Unsupported, corrupt or oversized snapshots generate a warning and leave database cookies usable; ambiguous/foreign/private contexts are omitted. Chromium profiles are also discoverable, but partitioned Chromium cookies are omitted and encrypted Chromium cookies on Windows are unsupported; manual paste is the fallback. Values and sensitive failure bodies must not appear in diagnostics.

**Test Access**, if retained, is a separate non-mutating diagnostic: establish both robots policies, check the website and optionally fetch and parse one known product ID. With no ID, the product API check is explicitly untested. Cookie presence or a public HTTP 200 does not prove account authentication. A failed required robots policy prevents subsequent website/product checks. Testing must not modify product jobs, discovery, archives or captures. In the current desktop controller, resuming an authentication pause requires a passed Test Access with the current credentials.

Lean implementation choice: an injected Cookie header provider can keep browser-specific import out of the core crawler. Supporting only manual paste would reduce scope and dependencies, but is a user-visible feature reduction from the current application.

Sources: [cookie matching and paste validation](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth.rs#L24), [browser import](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth.rs#L374), [Firefox current session](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/auth/firefox_session.rs), [Test Access](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L838).

## 10. Durable capture and crash recovery

SQLite and the filesystem do not share a transaction. Preserve the following ordering, or provide an equivalent protocol with the same recovery guarantees:

```text
function persist_validated_product(product, response):
    sha = SHA256(response.decoded_body)
    key = "{catalogue_uuid}/{target}/{id}/{sha}.json.gz"
    gzip_bytes = gzip(response.decoded_body)
    intent = {new UUID, target, id, fetched_at, api_url, safe_http,
              key, sha, decoded_size, compressed_size, gzip, archived_at}

    transaction(fenced): insert unfinished intent; job.state = persistence
    write gzip to private staging file; fsync it
    atomically publish key without replacing an existing archive
    sync relevant directories; read back, decompress, verify sizes and SHA-256
    transaction(fenced):
        require product identity and completeness
        insert product if not already present
        finalize intent
        job.state = successful
    commit
```

The current implementation verifies all archive files in a save batch **before** opening the final SQL transaction, then commits product rows, finalized intents and successful states together. Existing good products are not overwritten by a later capture. Store only bodies that have already passed response classification, identity and completeness checks. Validate archive paths and reject traversal/symlinks and size/hash mismatches. Do not hold a database transaction open across network requests or archive file writes.

```text
function recover_pending_save_intents():
    for intent in bounded UUID-ordered pages:
        if product already exists:
            mark intent finalized; continue
        archive = read_and_verify(intent.archive)
        if archive is absent:
            delete unfinished intent; make product job pending; continue
        if archive is corrupt:
            pause for storage/integrity repair
        product = parse_and_validate(archive.body, intent.id, intent.api_url)
        transaction(fenced): insert product, finalize intent,
                             mark job successful
```

**Single-writer safety:** An OS file lock prevents two live mutating owners. Each acquisition also installs a fresh owner UUID and increments a SQLite generation. Every authoritative write verifies the owner/generation inside its transaction and again before commit, rejecting stale writers. Process death releases the OS lock, permitting takeover and recovery without waiting for a lease timeout. The implementation rechecks ownership periodically and before request starts. A lock row without a real process lock is not equivalent.

Sources: [archive key, publication and verification](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/archive.rs#L82), [intent and commit](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L458), [ownership fencing](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L48), [recovery](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L107).

## 11. Reprocessing and shutdown

```text
function reprocess_saved_responses():
    acquire the same exclusive catalogue ownership and validate archives
    recover outstanding capture intents
    for each product with saved_parser_version != current_parser_version,
        in bounded keyset pages:
        body = read and verify its original archive
        result = parse_and_validate(body, product.id, saved_api_url)
        if result is complete and identity matches:
            atomically replace parser-derived fields and parser/schema version
            set reparsed_at = now
            preserve fetched_at, last_checked_at, archive/ref, HTTP info,
                discovery, product identity and successful state
        else if parser reports a product failure:
            keep the previous complete record and record the failure separately
        else if archive/storage is missing or corrupt:
            pause for storage repair
    release ownership
```

No website or API requests are made during this operation, and no new capture archive is created. Records already on the current parser version are skipped. The original stores results and a checkpoint by run; successful records are naturally skipped on a later reprocessing run because their parser version is now current.

On Stop, stop admitting new requests immediately and allow up to **five seconds** for active work to drain. Then hard-cancel remaining work, kill/reap parser subprocesses as needed, persist completed validated work, return uncompleted active claims to pending, finalize the run and release ownership. Abrupt process death must remain recoverable via the lock and intent protocol.

Sources: [reprocessing loop](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L610), [reprocess commit](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/storage/sqlite.rs#L644), [shutdown and run loop](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler.rs#L406).

## 12. Lean implementation boundary

A small implementation can use five replaceable pieces: **target adapter**, **HTTP/robots and cookie provider**, **scheduler/queue**, **pure product parser**, and **durable store/archive**. They can live in one crate and need no Tauri, TypeScript, OpenAI integration, classifier, search index, distributed queue or extra storefront abstraction. Preserve the target key in persisted identities so a later adapter does not collide.

For a practical first implementation, keep the queue and capture state in SQLite, use a bounded async scheduler with one write coordinator, and keep parsing pure behind an optional isolated worker. The exact crate choices, module boundaries and internal event transport are not externally observable. Keep the verified-archive/intent protocol, bounded work, per-origin robots pacing, target-wide cooldown, first-capture rule and safe cookie scope if behavior parity matters.

These are explicit reductions if chosen; they are **not** equivalent to the current crawler:

- Dropping the full extracted field contract or raw source fidelity in favor of title/description only.
- Treating a product 403 as a whole-target authentication failure, or treating all challenge pages as ordinary product failures.
- Ignoring robots rules/cooldowns, unbounded retries, or refetching successful products on `lastmod` changes.
- Skipping archives, crash recovery, owner fencing, parser reprocessing or hard parser termination.
- Omitting browser import or Test Access while claiming feature parity.
- Reusing a simplified schema against an existing KaijuCrawl catalogue without migration and preservation work.

### Source-observed edge case

The adapter accepts `https://drivethrurpg.com/...` as a website alias, including for sitemap URLs, but normal policy setup loads the `www` website origin and the API origin only. Because the HTTP layer requires a policy for the exact request origin, a nested sitemap on the bare-domain origin can be accepted during discovery and then pause with “Robots policy not loaded.” Product aliases are normalized to the canonical/API origins before fetching. A new implementation should make an explicit choice: reproduce this behavior for strict parity, or load and enforce a separate policy for each approved origin it actually requests. The latter is a deliberate correction, not behavior verified in this snapshot.

Sources: [approved hosts](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/targets/drivethru.rs#L34), [robots origins](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs#L255), [exact-origin policy check](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http.rs#L387).

## 13. Suggested conformance scenarios

These are fixture-based acceptance scenarios for the reimplementation; they describe expected behavior and do not claim tests were run for this document.

1. **Identity and discovery:** `00042` and `42` from several valid aliases create one target-scoped product job; foreign URLs, ID zero, malformed IDs and extra-host credentials create none. Cyclic nested sitemaps terminate within a run. Extension `image:loc` is ignored.
2. **Repeated runs:** Root/nested sitemaps are requested on both runs; a successful product is requested once total, even with a later `lastmod`; an unsuccessful product becomes eligible in the second run. A future persisted cooldown/due time is still honored.
3. **Completeness and isolation:** An exact matching ID with a full title/description succeeds. Missing full description, script-only description, wrong ID, two matching direct candidates, conflicting IDs, a nested recommendation match and malformed JSON all fail without a product row or archive.
4. **403 versus authentication:** Generic product 403 fails only that product with no immediate retry; another product can succeed. Explicit `sessionExpired=true` or `cf-mitigated: challenge` pauses the target. A challenged robots response prevents crawl setup. No failed response body appears in durable state or logs.
5. **Pacing:** A website robots delay does not delay API starts, and an API delay does not delay website starts. A target-wide Retry-After delays both; cancellation while waiting does not spend an attempt. Distinct target limiters remain independent.
6. **Retry budget:** A transient failure waits approximately 10 + job-ID jitter seconds before the first retry, honors a longer Retry-After, stops after five started attempts, and is eligible in a later run. Ordinary product 403 is not retried within that run.
7. **Metadata fidelity:** Preserve a high-precision decimal exactly, a zero sale price, optional-null fields, nested raw data, taxonomy labels, inline text (`together`), paragraph boundaries, resolved media links and direct relationship type/ID matching. Verify the repository's synthetic full-product fixture.
8. **Archive boundaries:** Inject a crash before intent, after intent before archive, after archive before SQL commit, and after commit. On restart, recover or requeue without duplicate success or lost successful records. Corrupt the compressed body, decoded length or SHA-256 and require a storage/integrity failure.
9. **Ownership and stop:** Two writers cannot mutate the same catalogue; a stale generation cannot commit. After process death a new owner takes over. Stop drains valid work within the grace period, releases claims and kills/reaps a stuck parser.
10. **Reprocessing:** A newer parser updates derived metadata from the archive without network traffic or changed observation times. Incomplete or wrong-identity reparsing retains the prior successful record; a corrupt archive pauses as a storage problem.
11. **Cookies and diagnostics:** Website/API pasted cookies do not cross origins. Imported domain/path/expiry/secure/container/partition rules are enforced, no secret values leak, and Test Access leaves queues, products and archives unchanged.

Reference tests: [contracts](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/contracts.rs), [HTTP](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/http/tests.rs), [end-to-end scheduler](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/src/scheduler/tests.rs#L430), [persistence](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/persistence.rs), [browser import](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/browser_import.rs), and [parser process](https://github.com/Rainbows4Blood/kaijucrawl/blob/f2ced97ac62f24e32abd51fd10b88fb7bda344af/tests/parser_process.rs).
