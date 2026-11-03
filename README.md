# Nekonomicon

A small, fast local book crawler built with Go and Wails. **DriveThruRPG must work end to end before any other storefront is implemented.**

The DTRPG desktop application is implemented: scoped Firefox/manual authentication, sitemap discovery, complete API responses in SQLite, recoverable runs, and local primary covers. See [implementation and validation notes](docs/IMPLEMENTATION.md) for measured results and platform limitations.

- [Implementation specification](docs/SPEC.md): scope, storage, pipeline, recovery, authentication, and acceptance criteria.
- [Interactive desktop mockup](docs/design/crawler.html): download/open this file in a browser; no server, dependencies, credentials, or network required. GitHub displays its source rather than running it.
- [Design handoff](docs/design/README.md): screen behavior and implementation notes.
- [Agent instructions](AGENTS.md): engineering constraints and validation expectations.
- [Example product response](docs/fixtures/dtrpg-product-584455.json): user-supplied response retained in full.

## Decisions

Go/Wails desktop application; local SQLite; complete product response JSON; local cover files. Discover IDs from robots-declared sitemaps (or the verified fixed fallback), fetch products in bounded parallel workers, save in batches through one writer, then download covers separately. Authentication behavior is ported from KaijuCrawl.

DMs Guild, Storytellers Vault, Ulisses eBooks, network databases, S3, classification, and catalog enrichment are deferred.

## Use

Open Nekonomicon, sign in to DriveThruRPG in Firefox, and import the matching
profile/container under **Connection & cookies**. Alternatively paste exact-host
website/API Cookie headers into the masked fields. **Test connection** validates
the real website and a product request before enabling product crawling.

Choose New products, Refresh all, or Retry failed, then Start. Pause waits for
current requests and commits received results. Resume continues the same run;
End run keeps committed data. Resume retains discovery progress while the app
stays open; restarting the app rediscovers the sitemap set. Robots-excluded
sitemaps are skipped with a visible count. Download covers also works independently without
product cookies. Local data contains an idle-only folder chooser; changing the
folder opens that catalog without moving existing files.

During the cover phase, **covers saved locally** counts distinct products with a
saved cover in this catalog, including after a restart. Resume checks each
product's image URL and local file again. Matching files are skipped. Products
checked and downloads this pass describe the current scan.

Defaults: `%LOCALAPPDATA%\Nekonomicon` on Windows;
`$XDG_DATA_HOME/nekonomicon` or `~/.local/share/nekonomicon` on Linux.
The database is `catalog.sqlite`; saved cover images are under
`covers/dtrpg/<product_id>/` inside that data folder.
On Windows, launching from a packaged application can redirect AppData into
that application's private storage. **Local data** displays the resolved
physical folder, which is the path to use in Explorer or the folder chooser.
Encrypted cookies live separately in the configuration directory, with the key
in Windows Credential Manager or Linux Secret Service. Do not commit or share
catalogs, cover folders, or credential files from authenticated sessions.

## Development

Go 1.26+, Node.js 22+, and Wails v2.17.1 are used. Windows needs WebView2.
Linux needs a C compiler, pkg-config, GTK 3, WebKitGTK 4.1, and an unlocked
Secret Service keyring for credential persistence.

```sh
go install github.com/wailsapp/wails/v2/cmd/wails@v2.17.1
npm --prefix frontend ci
npm --prefix frontend run check
go test ./...
go vet ./...
wails build
```

On Linux, use `go test -tags webkit2_41 ./...`,
`go vet -tags webkit2_41 ./...`, and `wails build -tags webkit2_41`.
The Windows executable is `build/bin/Nekonomicon.exe`. Frontend assets are
embedded directly; there is no frontend server or production npm dependency.

The opt-in `go run ./cmd/smoke -live -profile NAME -covers` command reads only DTRPG
cookies from the named Firefox profile's default container and checks a bounded
sample (at most eight sitemaps and three discovered products, plus a connection
probe, and optionally three covers). It verifies byte-identical SQLite storage
in a temporary catalog that is removed on exit. It prints no cookies or response
dumps and does not perform a full crawl.

Offline measurement: `go run ./cmd/measure -count 1000`. Storage-only benchmark:
`go test ./internal/store -run '^$' -bench BenchmarkStorage -benchtime=4000x -benchmem`.
Both use temporary local catalogs and the supplied fixture; neither contacts DTRPG.

## Repository reset

The fresh-start commit replaces the previous repository contents without rewriting history. Its parent contains the previous design. Revert the reset commit to restore that tree.
