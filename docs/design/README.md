# Crawler dashboard design handoff

These three interactive mockups record the visual direction for Nekonomicon's Tauri desktop app. They are design references, not application code or a functioning crawler.

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
- During a crawl, the storefront progress view has two numeric columns: **Queued** and **Catalogued**. Do not restore the earlier Pages and Listings columns.
- The product is called **Nekonomicon**. A catalog and wiki sources are part of the broader idea, but those screens have not been designed here.

## Setup view

- The four storefronts appear as individually selectable cards; selection is separate from each card's cookie setup.
- Each card presents the storefront name, last-crawl state, cookie state, and an action to manage cookies. The same dialog offers Firefox and manual import for the selected storefront.
- The full-recrawl switch applies to the selected storefronts. The working design interpretation is that a full recrawl revisits every listing; the exact behavior of a regular crawl remains to be defined.
- A storage status panel names each missing service and links to settings. The start button is enabled only when MongoDB and S3 are configured and at least one storefront is selected. A saved configuration is not presented as proof that the service is reachable.
- The initial selection, cookie states, and Firefox profile shown in the mockup are examples, not requirements about defaults or authentication.

## Storage settings view

- MongoDB takes a connection string and database name. Product responses and crawler state are stored here. The implementation requires a transaction-capable replica set or sharded cluster.
- S3 takes a bucket, region, optional prefix and optional custom S3-compatible endpoint. Credentials come from the AWS default provider chain or explicitly entered access key and secret, with an optional session token. The crawler stores each product’s primary cover image in S3; the product JSON remains in MongoDB.
- The services can be saved and tested independently. Testing is optional before the user presses **Start crawl**; the real backend performs its own preflight at start or resume, reports service failures, and starts no crawl if it cannot use the configured storage.
- Secret fields must be protected in the actual application. The mockup stores no field values, makes no network requests and passes only simulated readiness flags between screens.
- If storage becomes unavailable during a run, the intended behavior is to pause safely and show an actionable error; the active-run example does not yet depict that error state.

## Active crawl view

- The header identifies the current crawl and offers pause and stop controls; these controls are proposed UI, not a settled backend contract.
- The overview displays overall **Queued**, **Catalogued**, and **Issues** counts. The storefront table repeats queued and catalogued counts alongside each store's status.
- In this draft, **Queued** means discovered listings waiting to be catalogued, and failed items are shown separately under **Issues**. A dash for a storefront that has not started avoids implying its total is known. Confirm the precise treatment of in-progress items, retries, duplicates, and per-run versus lifetime counts during implementation.
- An active storefront shows its current task and a link to issue details. A compact activity feed shows recent events; the example exposes more rows and error details on demand.
- The issue-details example includes an image-download issue and a product that exceeded MongoDB's document-size limit. A product can be catalogued while its image is unavailable; image failures are surfaced as issues.
- **Queued** continues to count products awaiting cataloguing. A crawl remains active while image jobs are pending or retrying, with image work visible in activity rather than a new progress column.
- The sample shows one store completed, one active, and two waiting so the states can be reviewed together. It does not prescribe sequential crawling or actual counts, timings, errors, or retry policy.

## Visual and interaction notes

- Desktop-first, compact layout: narrow navigation rail, clear page heading, restrained panels, and easy-to-scan source rows. The HTML also reflows at narrower widths.
- The reference uses a neutral surface, purple accents, green completion indicators, and amber issue indicators, with light and dark themes. Status is also expressed in text rather than color alone.
- Keep selection and cookie management distinct, make issue details accessible without relying on hover, and use readable tabular figures for counts.
- An overall percentage is intentionally absent when the total amount of work is unknown. The queue and catalogue counts provide visible progress without inventing a denominator.
- The mockup includes a placeholder Catalog navigation item; it is not a specification for the catalog screen.

## Questions left for the implementer and product owner

- What distinguishes a regular crawl from a full recrawl, and how should each behave on its first run?
- Are cookies required for each source or optional? What manual paste format and Firefox profile-selection/error states should be supported on each target platform?
- How should queued, in-progress, completed, failed, and retried items be counted and deduplicated? Are counts scoped to a single run or the whole catalog?
- What are the intended pause, resume, stop, and retry semantics? How much crawl history should remain available?
- How should the storage-unavailable and recovery states appear during an active crawl? What migration flow should be offered when changing the MongoDB database for an existing catalogue?
- How should future storefronts and wikis be represented when their requirements are known?

## Credits

The mockups embed a small selection of [Lucide](https://lucide.dev/) icons. The applicable Lucide and Feather license notices are in [LUCIDE-LICENSE.txt](LUCIDE-LICENSE.txt).
