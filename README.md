# Nekonomicon

A planned desktop app for crawling and cataloguing tabletop RPG books from storefronts and, eventually, wikis. The crawler and backend will be written in Go, with Wails providing the desktop application framework.

## Crawler dashboard design

The repository currently contains the visual design handoff and a reference to the earlier KaijuCrawl crawler. Application source is not included in this repository yet.

- [Crawler setup mockup](docs/design/crawler-setup.html) — storefront selection, per-store cookie setup, the full-recrawl toggle, and the storage prerequisite.
- [Active crawl mockup](docs/design/crawler-active.html) — per-store queued and catalogued counts, issues, and activity.
- [Storage settings mockup](docs/design/crawler-settings.html) — MongoDB and S3 connection settings and example configuration states.
- [Design handoff](docs/design/README.md) — the agreed scope, interaction notes, and open decisions for implementation.

Open any of the HTML files directly in a modern browser. The mockups are self-contained, work offline, link to each other, and use example data only.

## Crawler implementation specification and reference

- [Crawler specification](docs/KaijuCrawl_Crawler_Reimplementation_Spec.md) — the current Nekonomicon contract: capture complete product responses into MongoDB in bounded batches, then download product images to S3. Standalone MongoDB and replica sets are supported; multi-document transactions are not required.
- [Historical Rust crawler reference](docs/reference/KaijuCrawl_Rust_Crawler_Reference.md) — the inspected KaijuCrawl behavior, parser rules and source links. Its durable queue, archive, transaction and recovery machinery is historical and must not override Nekonomicon's current specification.

The crawler keeps its working queue and product buffer in memory, writes batches of up to 1,000 documents, and saves periodic recovery checkpoints. Abrupt termination can lose unflushed or uncheckpointed progress; later work may be repeated. **Catalogued** counts confirmed saved products, while buffered products remain **Queued**. Images run as a subsequent phase and have their own status without adding another progress-count column.

