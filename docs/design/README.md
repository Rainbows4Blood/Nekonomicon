# Desktop design handoff

## Production workspace — 2026-11-06

The production UI in `frontend/` now uses the entire app window. It replaces the
centered utility sheet and promotional heading with a desktop shell. Default
window size is 1440 × 900; the minimum remains 390 × 560.

- The persistent left sidebar owns identity, Crawler / Data maintenance
  navigation and the local catalog folder. The native storefront selector and
  connection status belong inside the Crawler panel.
- The main workspace scrolls independently of the sidebar and bottom status bar.
  Its header names the current task. There is no maximum-width wrapper.
- Crawler commands are grouped above a flexible run overview and a connection
  inspector. Progress and phase indicators are separate from catalog totals;
  retry and cover-only runs explicitly mark excluded steps. Errors remain a
  bounded, pageable list below the overview, open initially.
- Maintenance uses the same layout primitives, with metadata extraction and
  progress in the main column, database structure in the inspector, and issues
  below. Pause/cancel/end controls belong to the shared maintenance progress area
  so schema operations do not appear to be metadata operations.
- At 1000 px and below the inspector stacks below the main content. At 760 px
  and below the sidebar becomes top navigation and the whole document scrolls.
  Buttons wrap, fields stack, and status/count text can wrap.

Neutral charcoal surfaces, restrained violet selection/actions, fine separators,
system fonts, tabular counts and inline vector icons provide the desktop visual
language. Colors use shared light/dark tokens. No external assets or production
dependencies were introduced. Shared shell, command bar, surface, inspector,
metrics and status primitives provide room for future authorized workspaces;
the current navigation includes only implemented features.

Navigation remains an accessible tablist, vertical on desktop and horizontal
on narrow screens. Arrow keys, Home/End, roving focus and focusable panels work
with native form controls and disclosure widgets. Loading keeps the app inert
and traps focus until inspection/closing finishes. Motion respects the reduced
motion preference. Existing source/run locks and explicit maintenance gates
are unchanged.

The historical `crawler.html` below remains an offline interaction illustration;
it is not the current visual specification. Its numbers are illustrative.

Catalog opening uses a blocking, keyboard-accessible inspection overlay. Opening
never initializes, upgrades, or backfills data. Incompatible schemas disable
crawling with a link to Data maintenance. The Crawler storefront
selector and Local data section remain available when no work owns the catalog.

Data maintenance offers separate Database structure and Product metadata tasks.
Apply database update explicitly initializes/upgrades tables; Extract metadata
processes missing/outdated/failed projections across the whole database.
Schema updates show stages and a Cancel database update button. Extraction
shows committed/total progress, current/pending counts, warnings, failures, and
Pause/Resume/End controls. Warning-only results count as complete. Issue lists
are paginated and rendered with textContent. Neither task requires sign-in.

Tabs support arrow keys, Home/End, roving tab focus, and correctly associated
panels. Existing run and maintenance ownership both lock source/folder changes.
Closing waits for cancellation/rollback or current batch completion before exit.

Open crawler.html directly in a browser. It is self-contained and works offline with no build step. All numbers and interactions are demonstrations; it performs no real authentication, filesystem access, or crawling.

## Visual direction

A calm desktop workspace: charcoal surfaces, violet primary action, readable tabular counts, restrained cat-inspired identity. Follow system light/dark appearance. Prefer whitespace and alignment over charts and ornamental metric cards. The main task is starting and monitoring a crawl.

## Interaction

The top preview selector explores Ready, Discovering, Fetching, Covers, Paused, Complete, Errors, and Authentication blocked. This selector and the preview banner are mockup-only. Start advances through simulated stages with the Advance demo button; Pause/Resume retains the simulated phase. End run preserves the displayed catalog count. Production transitions must be driven by the Go engine.

Mode and covers preference lock during an active or paused run. The connection panel illustrates profile/container selection, manual website/API cookies, and a test result. These inputs are never persisted. Do not implement browser import by reading arbitrary paths in frontend code; bind the Go auth service. Activity expands to bounded, useful errors and retry actions. Download covers is available as a standalone action when no run is active.

Catalogued counts all saved products; progress counts outcomes within the current run. During discovery, the total is unknown. During cover downloads, product counters stay stable and the progress label changes to covers. Use native controls, visible labels, keyboard focus, and readable narrow layouts.

## Implementer notes

Treat docs/SPEC.md as the behavioral authority. Use frontend state snapshots emitted by Go at no more than 4 Hz; the demo JavaScript is disposable. Avoid per-product DOM updates and loading complete JSON into the frontend. Opening local storage only inspects compatibility; initialization and upgrades require explicit maintenance. The data-folder chooser is implemented as an idle-only action in Local data.

The implemented Wails screen lives in `frontend/`. It uses Go snapshots at 4 Hz,
skips unchanged snapshots, masks and clears pasted cookies, and pages persisted
errors 50 at a time. Folder selection is implemented in Local data and requires
ending any unfinished run first. The standalone mockup stays unchanged and does
not import credentials or crawl. Native Windows checks covered desktop/narrow
layouts and keyboard focus; the production renderer is tested across every run
state. Linux light appearance was checked under WSLg with software rendering.

Robots exclusions appear as a quiet skipped-sitemap count that remains visible
during fetching. They do not pause a run. Discovery progress is retained across
pause/resume while the process stays open; reopening the app rediscovers it.

The production screen now offers DriveThruRPG and DM’s Guild through a labeled native selector. It locks during active/paused runs and connection operations. Switching resets connection readiness, clears manual cookie fields, updates host hints and the test product ID, and displays only that store’s catalog/errors. Keyboard focus returns to the selector after switching. The original offline mockup remains a DTRPG illustration. Data maintenance is implemented; D&D browsing is now implemented; other catalog browsing, remote storage, and infrastructure settings remain out of scope. No external assets or icon package are required by this mockup.

Data maintenance also offers **D&D edition groups**, with checked/pending counts,
Build/Resume grouping, and the shared committed progress controls. Metadata and
grouping cannot overlap or replace a paused task. The issues disclosure has a
labeled task selector so grouping ambiguity and failures can be reviewed without
mixing them with metadata diagnostics. B/X's Basic-family meaning and conservative
official-status behavior are explained beside the task. Groups are rebuilt across both
storefronts without sign-in. Existing responsive and keyboard behavior is retained.

Data maintenance is independent of the Crawler storefront selection. Counts,
committed progress and issue pages include both storefronts, with the source
named beside each issue. Its footer reads All storefronts. Switching tabs
preserves the Crawler selection and its existing run and connection locks.

## D&D workspace — 2026-11-07

Navigation now contains Crawler, Data maintenance and D&D. D&D activates the
fantasy theme across the shell, with a dark green sidebar, gold trim, parchment
list/detail surfaces, serif book titles and the embedded library painting.
Leaving D&D restores the default light/dark workspace. The theme adds no remote
fonts, styles or imagery. Artwork and its generation prompt are recorded in
[ARTWORK.md](ARTWORK.md).

The library has a bounded, paginated list with small local covers, title, publisher,
storefront, edition badges, official status and release date. Search scope, edition,
official status and four sort choices use labeled native controls. Details replace
the list in the same tab and provide a focused heading and Back control; Back
retains filters/page/focus. Empty, unindexed, incompatible-schema, image-unavailable,
loading and failed-query states have explicit messages. Async responses from old
folders or superseded searches are discarded.

Data maintenance gains Browsing & search index with committed progress, independent
Pause/Resume/End ownership and its own issue selector. Indexing covers all saved
responses; only grouped D&D products appear in the library. No work starts merely
by opening a catalog or selecting a tab.


## Storefront search and OpenAI connection (2026-11-07)

D&D adds a Storefront filter beside the edition and official-status controls.
It defaults to Both storefronts, resets pagination when changed, and does not alter
Crawler selection. Maintenance still applies to the whole database. A failed count
refresh shows a notice without replacing the completed task state or offering a
spurious Resume; Refresh counts retries the read.

A compact OpenAI disclosure in the shared sidebar provides a saved-account picker,
Continue with ChatGPT, Cancel sign-in, Sign out and account-access settings. The
browser owns authentication and consent; the app displays only connection state and
validated account labels. Controls remain available independently of catalog work.
It follows the active workspace theme and stacks above Local data on narrow windows.


## D&D enrichment dashboard

D&D's secondary tablist switches between Library, Adventures and Enrichment without leaving
the fantasy workspace. Arrow keys/Home/End and labeled panels work from the keyboard.
Enrich is the single start/retry action. Stop appears during a run. The page explains
what leaves the computer, shows up to 16 active books with their source/web stages,
five enrichment completion counts plus eligible and pending books, committed run outcomes, optional-cover information and paginated
results/evidence. Unknown endpoints display as Unknown and successful unknown
lookups count as enriched, separately from operational issues. Completed unknowns
also appear on book details. The intro identifies fast mode as requested and notes
that OpenAI may use standard processing. No model/worker/task configuration is exposed.
Results also appear under Adventure enrichment on the book details page.
Usage/rate-limit waits keep Stop enabled and Enrich disabled. Waiting books show
Waiting for OpenAI, with an automatic retry time or a message that another retry
is in progress. The run heading shows Waiting for OpenAI when all active books
are waiting, and during an access-check cooldown. Waiting does not add an issue.

Setting, location and in-world date results appear in the dashboard and book
details with evidence. A named setting may coexist with Agnostic (any campaign
setting). Unknown (enriched) and Not enriched yet remain distinct. Dates are
explicitly labeled In-world dates; named calendars and settings remain attached
to their dates/locations. The seven summary tiles wrap at narrow widths.

Startup interface failures show Could not start the interface, hide the spinner,
and retain a working Abort and close button. Closing waits for safe backend cleanup
even when the catalog has already opened. The loading screen must not imply that
a missing packaged module is slow catalog work.

## Enriched adventure filters — 2026-11-08

Adventures has a native Adventure filters disclosure to keep the default search
compact. Fieldsets group players, levels, settings, locations and in-world dates.
Each numeric endpoint has labeled From/To inputs; settings, locations, dates and
calendars use multi-select lists populated from saved enrichment. Selections match
any chosen value within a list, and combine across different lists. Completion-status selectors separate unknown, unfinished and fully known
results, with an explicit Agnostic setting option. An active-control count stays
visible when collapsed. Reset clears all filters; Back preserves them and the page.

The sort selector includes each endpoint and text category in both directions.
Help explains numeric endpoints, first-alphabetical-entry sorting and source-text
date ordering. Adventure results display enrichment in dedicated table columns, safely rendered
as text. Library retains only the general filters and compact book rows.
The fieldsets stack at narrow widths; all controls use native keyboard behavior.

The Adventures table has columns for Book, Min players, Max players, Min level,
Max level, Settings, Locations, In-world dates, Calendars and Release date. Book
buttons open the existing details view; Back preserves that tab's filters/page.
Column headers toggle direction and expose aria-sort. The table scrolls within a
labeled, focusable region on narrow windows. Library, Adventures and Enrichment
use a shared three-tab keyboard navigation (arrows/Home/End), with independent
Library/Adventures state. Neither Library filters nor tab selection limits Enrich.
The dashboard explains that books without covers can still be enriched.

Multi-select lists use native keyboard behavior and display six rows. Help explains
Ctrl/Command selection, Ctrl + Space, exact values and the empty-selection meaning.
Each list has Previous/Next choice-page buttons, Clear selection, selected count,
loading/empty feedback and a Retry values button after failure. Choices are paged
100 at a time; selected values from other pages stay pinned in an optgroup.
No free-text enrichment filter input remains. General library search is unchanged.
