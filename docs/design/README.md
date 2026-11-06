# Desktop design handoff

## Production workspace — 2026-11-06

The production UI in `frontend/` now uses the entire app window. It replaces the
centered utility sheet and promotional heading with a desktop shell. Default
window size is 1440 × 900; the minimum remains 390 × 560.

- The persistent left sidebar owns identity, the native storefront selector,
  Crawler / Data maintenance navigation and the local catalog folder.
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
crawling with a link to Data maintenance. The shared storefront
selector and Local data section remain available when no work owns the catalog.

Data maintenance offers separate Database structure and Product metadata tasks.
Apply database update explicitly initializes/upgrades tables; Extract metadata
processes missing/outdated/failed projections for the selected storefront.
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

The production screen now offers DriveThruRPG and DM’s Guild through a labeled native selector. It locks during active/paused runs and connection operations. Switching resets connection readiness, clears manual cookie fields, updates host hints and the test product ID, and displays only that store’s catalog/errors. Keyboard focus returns to the selector after switching. The original offline mockup remains a DTRPG illustration. Data maintenance is implemented; catalog browsing, remote storage, and infrastructure settings remain out of scope. No external assets or icon package are required by this mockup.
