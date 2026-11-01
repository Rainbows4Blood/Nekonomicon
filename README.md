# Nekonomicon

A small, fast local book crawler built with Go and Wails. **DriveThruRPG must work end to end before any other storefront is implemented.**

Implementation is underway. The Go core currently provides scoped Firefox/manual authentication, bounded HTTP and sitemap discovery, and a transactional SQLite catalog. The desktop application and recovery pipeline are being added. See [implementation and validation notes](docs/IMPLEMENTATION.md) for the acceptance status.

- [Implementation specification](docs/SPEC.md): scope, storage, pipeline, recovery, authentication, and acceptance criteria.
- [Interactive desktop mockup](docs/design/crawler.html): download/open this file in a browser; no server, dependencies, credentials, or network required. GitHub displays its source rather than running it.
- [Design handoff](docs/design/README.md): screen behavior and implementation notes.
- [Agent instructions](AGENTS.md): engineering constraints and validation expectations.
- [Example product response](docs/fixtures/dtrpg-product-584455.json): user-supplied response retained in full.

## Decisions

Go/Wails desktop application; local SQLite; complete product response JSON; local cover files. Discover IDs from robots-declared sitemaps, fetch products in bounded parallel workers, save in batches through one writer, then download covers separately. Authentication behavior is ported from KaijuCrawl.

DMs Guild, Storytellers Vault, Ulisses eBooks, network databases, S3, classification, and catalog enrichment are deferred.

## First implementation milestone

Prove authenticated DTRPG sitemap discovery and API fetching into SQLite before wiring up the full UI. Measure fetch, decode, and commit time separately. Then add recovery, covers, and Wails integration. See the spec for the completion gate.

## Development

Go 1.26 or newer is required. Run `go test ./...` and `go vet ./...`.
The opt-in `go run ./cmd/smoke -live -profile NAME` command reads only DTRPG
cookies from the named Firefox profile's default container and checks a bounded
sample (at most eight sitemaps and three discovered products, plus a connection
probe). It prints no cookies or response dumps. It does not perform a full crawl.

## Repository reset

The fresh-start commit replaces the previous repository contents without rewriting history. Its parent contains the previous design. Revert the reset commit to restore that tree.
