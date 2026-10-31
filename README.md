# Nekonomicon

A planned Tauri desktop app for crawling and cataloguing tabletop RPG books from storefronts and, eventually, wikis.

## Crawler dashboard design

The repository currently contains the visual design handoff and a reference to the earlier KaijuCrawl crawler. The crawler and desktop app have not been implemented yet.

- [Crawler setup mockup](docs/design/crawler-setup.html) — storefront selection, per-store cookie setup, and the full-recrawl toggle.
- [Active crawl mockup](docs/design/crawler-active.html) — per-store queued and catalogued counts, issues, and activity.
- [Design handoff](docs/design/README.md) — the agreed scope, interaction notes, and open decisions for implementation.

Open either HTML file directly in a modern browser. The mockups are self-contained, work offline, link to each other, and use example data only.

## Crawler implementation reference

- [KaijuCrawl crawler specification](docs/KaijuCrawl_Crawler_Reimplementation_Spec.md) — an as-implemented reference for the earlier Rust crawler. It documents DriveThruRPG and first-successful-capture behavior; Nekonomicon's design additionally calls for four storefronts and a full-recrawl option.
