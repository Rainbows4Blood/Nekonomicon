# Crawler dashboard design handoff

These three interactive mockups record the visual direction for Nekonomicon's planned Go and Wails desktop app. They are design references, not application code or a functioning crawler.

The [crawler specification](../KaijuCrawl_Crawler_Reimplementation_Spec.md) defines the current capture, phase and recovery behavior. The [historical Rust crawler reference](../reference/KaijuCrawl_Rust_Crawler_Reference.md) documents the earlier implementation; its storage and recovery mechanisms are not requirements for these screens.

## Preview the screens

1. Open [crawler-setup.html](crawler-setup.html) in a modern browser. **Start crawl** is initially disabled because storage is not configured. Select storefronts, open **Set up cookies**, switch between Firefox and manual paste, and toggle **Full recrawl**.
2. Choose **Configure storage** to open [crawler-settings.html](crawler-settings.html). Enter placeholder MongoDB and S3 settings and save each one. Follow **Back to crawler**: with both saved and at least one storefront selected, **Start crawl** displays an example activity state. Try removing one configuration to see the gate again. **Test connection** is simulated and does not contact a service.
3. Open [crawler-active.html](crawler-active.html) for the more detailed active-run view. Try **Pause**, **View issues**, and **Show more**. This independent example can be opened without starting a crawl; it uses sample data and does not bypass the real application's storage gate.

The links above the mockups switch between the three references. No build step, server, internet connection, or installed dependencies are needed. Example Firefox profiles, product identifiers, progress, and errors are fictional. Cookie import, connection tests and saving settings are simulated: do not use real credentials in the mockups. Only example configured/not-configured flags are passed in links; entered field values are not persisted or placed in the URL.

## Agreed scope

- The initial storefronts are **DriveThruRPG**, **DMs Guild**, **Storytellers Vault**, and **Ulisses ebooks**.
- The user can choose which storefronts to crawl and has a **Full recrawl** toggle. Keep configuration light.
- Each storefront has cookie setup with two paths: **manual paste** and **import from Firefox**.
- MongoDB and S3 are both required. Their connection settings are accessible from **Settings → Storage**, and crawling is blocked until both configurations are saved. The backend independently enforces the requirement and checks connectivity before a run.
- During a crawl, the storefront progress view has two numeric columns: **Queued** and **Catalogued**. Only confirmed saved product documents count as catalogued; products held in the memory buffer remain queued. Do not restore the earlier Pages and Listings columns.
- Capture product documents first, then download their images in a separate phase. Show the current phase in status and activity text without adding another numeric column.
- The product is called **Nekonomicon**. A catalog and wiki sources are part of the broader idea, but those screens have not been designed here.

## Setup view

- The four storefronts appear as individually selectable cards; selection is separate from each card's cookie setup.
- Each card presents the storefront name, last-crawl state, cookie state, and an action to manage cookies. The same dialog offers Firefox and manual import for the selected storefront.
- The full-recrawl switch applies to the selected storefronts. A regular crawl rescans sitemaps and skips complete saved products. The working design interpretation is that a full recrawl revisits every listing; its overwrite and history-retention behavior remains to be defined.
- A storage status panel names each missing service and links to settings. The start button is enabled only when MongoDB and S3 are configured and at least one storefront is selected. A saved configuration is not presented as proof that the service is reachable.
- The initial selection, cookie states, and Firefox profile shown in the mockup are examples, not requirements about defaults or authentication.

## Storage settings view

- MongoDB takes a connection string and database name. Saved product responses and periodic recovery checkpoints are stored here; the active queue and unsaved product buffer stay in memory. Standalone MongoDB and replica sets are supported. Do not require transaction support or a replica set during configuration or preflight.
- S3 takes a bucket, region, optional prefix and optional custom S3-compatible endpoint. Credentials come from the AWS default provider chain or explicitly entered access key and secret, with an optional session token. The crawler stores each product’s primary cover image in S3; the product JSON remains in MongoDB.
- The services can be saved and tested independently. Testing is optional before the user presses **Start crawl**; the real backend performs its own preflight at start or resume, reports service failures, and starts no crawl if it cannot use the configured storage.
- Secret fields must be protected in the actual application. The mockup stores no field values, makes no network requests and passes only simulated readiness flags between screens.
- If MongoDB becomes unavailable during a run, pause new metadata work and show an actionable error. Keep the bounded unsaved buffer while the process remains alive, and do not report unconfirmed writes as catalogued. An S3 outage pauses the later image phase without invalidating saved products; document capture does not make S3 calls. The active-run example does not yet depict these error states.

## Capture and recovery behavior

The document phase uses one bulk writer and bounded batches of up to 1,000 product documents. Flush earlier at the 16 MiB batch-byte ceiling, after five seconds, or when work drains. These are internal implementation defaults; they do not require more user-facing configuration. The image phase begins after document capture has finished and its remaining buffer has been flushed.

The working queue is volatile, with recovery checkpoints approximately every 30 seconds. Pause and stop should attempt to flush completed products and save a checkpoint. An abrupt exit can lose the unsaved buffer or progress since the last checkpoint, and a later run may repeat work. The interface must not promise recovery of every in-flight item or display buffered products as saved.

## Active crawl view

- The header identifies the current crawl and offers pause and stop controls; these controls are proposed UI, not a settled backend contract.
- The overview displays overall **Queued**, **Catalogued**, and **Issues** counts. The storefront table repeats queued and catalogued counts alongside each store's status.
- **Queued** means discovered products still awaiting a confirmed save, including in-progress requests, scheduled retries and validated products in the memory buffer. **Catalogued** advances only after MongoDB confirms the corresponding product writes; replaying an already saved identity must not increase it. Failed items are shown separately under **Issues**. A dash for a storefront that has not started avoids implying its total is known. Per-run versus lifetime presentation remains an open design choice.
- An active storefront shows its current task and a link to issue details. A compact activity feed shows recent events; the example exposes more rows and error details on demand.
- The issue-details example includes an incomplete product response and a product that exceeded MongoDB's document-size limit. In the later image phase, a product can remain catalogued while its image is unavailable; image failures are surfaced as issues.
- After the document phase, show **Downloading images** or equivalent phase text. **Queued** continues to count products awaiting cataloguing, so it can be zero while images are still pending or retrying. The run remains active until the image phase finishes or is stopped; image work appears in status and activity rather than a new progress column. Image failures do not reduce the confirmed **Catalogued** count.
- The sample shows one store's documents saved, one store actively capturing, and two waiting so the states can be reviewed together. Its activity feed shows a saved document batch and notes that images follow after capture. It does not prescribe sequential crawling or actual counts, timings, errors, or retry policy.

## Visual and interaction notes

- Desktop-first, compact layout: narrow navigation rail, clear page heading, restrained panels, and easy-to-scan source rows. The HTML also reflows at narrower widths.
- The reference uses a neutral surface, purple accents, green completion indicators, and amber issue indicators, with light and dark themes. Status is also expressed in text rather than color alone.
- Keep selection and cookie management distinct, make issue details accessible without relying on hover, and use readable tabular figures for counts.
- An overall percentage is intentionally absent when the total amount of work is unknown. The queue and catalogue counts provide visible progress without inventing a denominator.
- The mockup includes a placeholder Catalog navigation item; it is not a specification for the catalog screen.

## Questions left for the implementer and product owner

- How should a full recrawl replace existing captures and retain their history?
- Are cookies required for each source or optional? What manual paste format and Firefox profile-selection/error states should be supported on each target platform?
- Should the confirmed product counts be presented for the current run, the whole catalog, or both? How should previously saved products be distinguished during a full recrawl?
- How should pause, resume and stop feedback communicate buffered work and best-effort checkpoints? How much crawl history should remain available?
- How should the storage-unavailable and recovery states appear during an active crawl? What migration flow should be offered when changing the MongoDB database for an existing catalogue?
- How should future storefronts and wikis be represented when their requirements are known?

## Credits

The mockups embed a small selection of [Lucide](https://lucide.dev/) icons. The applicable Lucide and Feather license notices are in [LUCIDE-LICENSE.txt](LUCIDE-LICENSE.txt).

