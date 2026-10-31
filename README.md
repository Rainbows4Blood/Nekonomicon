# Nekonomicon

A planned Tauri desktop app for crawling and cataloguing tabletop RPG books from storefronts and, eventually, wikis.

## Crawler dashboard design

The repository currently contains the visual design handoff and a reference to the earlier KaijuCrawl crawler. The crawler and desktop app have not been implemented yet.

- [Crawler setup mockup](docs/design/crawler-setup.html) — storefront selection, per-store cookie setup, the full-recrawl toggle, and the storage prerequisite.
- [Active crawl mockup](docs/design/crawler-active.html) — per-store queued and catalogued counts, issues, and activity.
- [Storage settings mockup](docs/design/crawler-settings.html) — MongoDB and S3 connection settings and example configuration states.
- [Design handoff](docs/design/README.md) — the agreed scope, interaction notes, and open decisions for implementation.

Open any of the HTML files directly in a modern browser. The mockups are self-contained, work offline, link to each other, and use example data only.

## Crawler implementation reference

- [Crawler specification](docs/KaijuCrawl_Crawler_Reimplementation_Spec.md) — Nekonomicon's MongoDB product-response and S3 product-image requirements followed by an as-implemented reference for the earlier Rust crawler. The reference documents DriveThruRPG and first-successful-capture behavior; Nekonomicon's design additionally calls for four storefronts and a full-recrawl option.
