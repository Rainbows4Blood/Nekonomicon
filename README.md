# Nekonomicon

A small, fast local book crawler built with Go and Wails. Supports **DriveThruRPG and DM’s Guild**.

The desktop application includes: scoped Firefox/manual authentication, sitemap discovery, complete API responses in SQLite, structured product metadata, indexed D&D edition groups, a searchable D&D library with book details, recoverable runs, local primary covers, and explicit Data maintenance. See [implementation and validation notes](docs/IMPLEMENTATION.md) for measured results and platform limitations.

The frontend uses a full-window desktop workspace: persistent navigation
sidebar, Crawler storefront selector and command bar, run overview, connection inspector, and shared status
bar. Data maintenance and D&D have their own workspaces within the same shell. Layouts adapt
to narrow windows and system light/dark appearance.

- [Implementation specification](docs/SPEC.md): scope, storage, pipeline, recovery, authentication, and acceptance criteria.
- [Original interactive mockup](docs/design/crawler.html): historical interaction reference, predating the full-window redesign; no server, dependencies, credentials, or network required. GitHub displays its source rather than running it.
- [Design handoff](docs/design/README.md): screen behavior and implementation notes.
- [Agent instructions](AGENTS.md): engineering constraints and validation expectations.
- [Example product response](docs/fixtures/dtrpg-product-584455.json): user-supplied response retained in full.

## Decisions

Go/Wails desktop application; local SQLite; complete product response JSON; local cover files. Discover IDs from robots-declared sitemaps (or the verified fixed fallback), fetch products in bounded parallel workers, save in batches through one writer, then download covers separately. Authentication behavior is ported from KaijuCrawl.

Storytellers Vault, Ulisses eBooks, network databases, S3, and general classification are deferred. Adventure range enrichment is available in D&D. Fixed D&D edition groups are supported through Data maintenance.

## Use

Choose a storefront on the Crawler page, sign in to that storefront in Firefox, and import the matching
profile/container under **Connection & cookies**. Alternatively paste exact-host
website/API Cookie headers into the masked fields. **Test connection** validates
the real website and a product request before enabling product crawling.

Cookies, connection checks, catalog counts, errors and cover files belong to the
selected storefront. End any unfinished run before switching stores. Reopening a
catalog with an unfinished run selects its storefront automatically.

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
`covers/<dtrpg|dmsguild>/<product_id>/` inside that data folder.
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
Go tests verify that the embedded frontend includes all referenced modules and
assets. To additionally exercise those embedded bytes in headless Chromium, set
`NEKONOMICON_PLAYWRIGHT` to an installed Playwright package and run
`go test . -run TestPackagedFrontendBrowser -count=1`.

If an interface file fails to load, startup shows **Could not start the interface**
and keeps **Abort and close** available. Use a fresh application build; this error
does not require a catalog repair or another indexing run.

Opening a catalog only checks compatibility. It does **not** initialize the
schema, upgrade tables, or extract existing responses. A new or older catalog
shows **Database needs maintenance before crawling**. Open **Data maintenance**
and choose **Apply database update**, then **Extract metadata** for saved responses across both
storefronts. These are separate actions.
Crawling becomes available after the schema update; historical backfilling can
be done later. New and refreshed products receive metadata during the crawl.

All Data maintenance actions, counts, and issues cover the whole database,
independently of the Crawler storefront selection. Maintenance shows committed
progress, warnings and failed extractions. Pause
retains committed batches and Resume continues after a restart. End maintenance
releases the catalog while keeping completed work. Crawling and maintenance
cannot overlap; end an unfinished crawl before extracting old metadata.
Warnings preserve useful fields; failed extraction preserves the raw response
and excludes stale metadata from the current-data view. No sign-in is required.

Schema updates are transactional and cancellable from the maintenance tab.
The old v1-to-v2 table copy still requires time and free space; v2-to-v3 adds
metadata tables without copying product JSON. Earlier application versions
cannot open the upgraded catalog. Closing waits for active work to settle.

The opt-in `go run ./cmd/smoke -live -source dmsguild -profile NAME -covers` command reads only the selected storefront’s
cookies from the named Firefox profile's default container and checks a bounded
sample (at most eight sitemaps and three discovered products, plus a connection
probe, and optionally three covers). It verifies byte-identical SQLite storage
in a temporary catalog that is removed on exit. It prints no cookies or response
dumps and does not perform a full crawl.

Offline measurement: `go run ./cmd/measure -count 1000`. Storage-only benchmark:
`go test ./internal/store -run '^$' -bench BenchmarkStorage -benchtime=4000x -benchmem`.
Both use temporary local catalogs and the supplied fixture; neither contacts a storefront.

## Repository reset

The fresh-start commit replaces the previous repository contents without rewriting history. Its parent contains the previous design. Revert the reset commit to restore that tree.

D&D groups are available in **Data maintenance**. Choose **Apply database update**
for schema v4, then **Build D&D groups** for the whole database. This materializes
nine edition groups and an official-product flag with recorded evidence, using
local responses only. Pause/Resume/End work independently of metadata extraction.
New crawls update memberships automatically. B/X includes Basic, BECMI and Rules
Cyclopedia. Ambiguous editions remain unresolved; see [grouping rules and
limitations](docs/GROUPS.md). The D&D workspace lists products in these edition groups across both stores.

## D&D library

Open **Data maintenance**, choose **Apply database update** for schema v5, then
**Build D&D browsing index**. This explicit task indexes saved responses across
both storefronts using the shared metadata extractor and edition rules; it does
not require a prior metadata/group backfill. Pause/Resume/End retain committed
work. New crawls keep the index current automatically.

The **D&D** page switches the app to a fantasy library theme. Browse 50 books per
page, filter by storefront, edition and All / Official only / Non-official only, and sort
Name or Release date in either direction. Search Name only or Name & description;
search matches all entered words, ignoring case and accents. Release date uses
saved dateAvailable (the storefront availability date, not an inferred original
print date); missing/unrecognized dates sort last. Unknown official status is
included only under All. Titles are sorted with Unicode case/accent folding.

Click a book for its description, publisher, release date, edition memberships,
creators, formats, files and available product metadata. Prices are displayed as
euros under the existing display assumption, without conversion. Back restores
the list's filters and page. Saved covers appear as local thumbnails; unavailable
covers use a placeholder. The library does not fetch covers or other remote content.
Use **Download covers** in Crawler to save missing images. **View on storefront**
opens the canonical product page in your browser.

The library includes products with at least one supported edition membership,
including supplements, tools and bundles under the existing grouping rules.
Ambiguous edition evidence remains unresolved. Entries from the two stores remain
separate, even when they share an ID or title. Pending indexing is shown explicitly;
refresh the list after catalog changes. Opening never upgrades or backfills data.


### OpenAI connection

Open **OpenAI** in the sidebar and choose **Continue with ChatGPT**. Finish sign-in
and consent in your system browser, then return to the app. You can select a saved
account to sign in again, add another account, cancel a pending sign-in, or sign out
of the active account. Eligible ChatGPT plans can authorize future AI requests;
identity-only sign-in is shown separately from permission to use the plan.

Credentials are encrypted outside the catalog, with the key in the OS credential
store. Sign-out attempts remote revocation and clears local tokens while retaining
the account registration for next time. **Manage ChatGPT access & usage** opens the
account's settings. Signing in sends no catalog data. Enrichment starts only when you press Enrich.

Catalogs from before adventure enrichment need **Data maintenance → Apply database
update**, then **Build D&D browsing index** to derive adventure eligibility. If a
count refresh fails after maintenance, completed work stays complete: use **Refresh
counts**, without rerunning the indexing task.


### Adventure enrichment

Open **D&D → Enrichment** and press **Enrich**. It runs every missing player-count,
character-level, setting, location and in-world date enrichment on indexed D&D adventures across both storefronts,
independently of Library filters. Up to 16 books run concurrently. The dashboard
shows each active book and stage, progress, enriched books (including unknown
values), operational issues, coverage and saved evidence.

The fixed model is GPT-6.1 Terra, with fast mode requested for both passes. OpenAI
may serve requests at standard speed depending on availability. Each request includes the complete saved product
JSON and its local cover (resized to fit 1600 pixels, with the whole image retained).
A storefront-only pass runs first. Only unresolved fields reach a second pass with
web search available. Existing populated fields are preserved; unsupported facts
remain null. Player counts exclude the GM. No conventional party size or level
limit is invented. Results also appear on book details.

Enrichment uses the connected ChatGPT account's allowance and sends the selected
records and covers to OpenAI. Missing/invalid covers are reported without a model
request; download them through Crawler first. Stop retains committed results.
After stopping or restarting, Enrich selects unfinished enrichments again. A
successful lookup with null/unknown endpoints counts as enriched and is not
repeated for that source revision. Failed or interrupted requests remain retryable. Crawling,
maintenance, catalog/account changes and enrichment cannot overlap. Refreshing a
source response makes its older enrichment stale until recomputed.

OpenAI usage and rate limits pause new requests instead of stopping enrichment.
Retries back off from 2 seconds to a maximum of 5 minutes, with jitter; a longer
server-requested wait is honored. One request checks availability before the other
books resume. The dashboard shows which books are waiting and the next retry time.
Retries continue until OpenAI accepts the request or you press Stop. Saved results
are retained, and these waits do not count as issues. Account/access errors still
need attention. No additional database update is required for this behavior.

The completion-tracking change requires **Apply database update** in Data
maintenance (schema v8). Opening a catalog does not migrate it. Existing known
results are preserved. Older unknown results may need one follow-up pass because
the previous format did not distinguish a finished lookup from an interrupted
storefront checkpoint. No browsing-index rebuild is needed for this v7 → v8 update.

The response parser retains completed answer events throughout the stream and
separates progress commentary from the final JSON. The final completion event can
have an empty output list even though the answer was already delivered. If an
earlier run saved "incomplete structured output" or "completed without a final
answer", press **Enrich** again to retry those books. Valid unknown results count
as enriched. These parser fixes need no additional database update.

Settings, locations and in-world dates require **Data maintenance → Apply database
update** (schema v9), then **D&D → Enrichment → Enrich**. Existing player/level
results, including completed unknowns, are kept; only missing enrichments run.
No browsing-index rebuild is needed for this v8 → v9 update. New or refreshed
adventures use the same five enrichments.

Settings can name several worlds and additionally be **Agnostic** when the source
explicitly supports any campaign setting. For example, Forgotten Realms plus
explicit adaptability records both. Missing information means **Unknown**, never
Agnostic. Rules-system compatibility alone does not identify a campaign setting.
Locations list places where the adventure takes place, with their setting when
supported. Dates preserve the fictional calendar, year, range or relative wording
from the evidence; publication dates are never substituted. Unsupported calendar
or setting associations stay unknown. Each enrichment has its own evidence and
completion count in the dashboard, with results also shown on book details.
