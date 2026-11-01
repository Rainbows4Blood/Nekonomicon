# Desktop design handoff

Open crawler.html directly in a browser. It is self-contained and works offline with no build step. All numbers and interactions are demonstrations; it performs no real authentication, filesystem access, or crawling.

## Visual direction

A compact, calm desktop utility: charcoal surfaces, violet primary action, readable tabular counts, restrained cat-inspired identity. Follow system light/dark appearance. Prefer whitespace and alignment over charts and ornamental metric cards. The main task is starting and monitoring a crawl.

## Interaction

The top preview selector explores Ready, Discovering, Fetching, Covers, Paused, Complete, Errors, and Authentication blocked. This selector and the preview banner are mockup-only. Start advances through simulated stages with the Advance demo button; Pause/Resume retains the simulated phase. End run preserves the displayed catalog count. Production transitions must be driven by the Go engine.

Mode and covers preference lock during an active or paused run. The connection panel illustrates profile/container selection, manual website/API cookies, and a test result. These inputs are never persisted. Do not implement browser import by reading arbitrary paths in frontend code; bind the Go auth service. Activity expands to bounded, useful errors and retry actions. Download covers is available as a standalone action when no run is active.

Catalogued counts all saved products; progress counts outcomes within the current run. During discovery, the total is unknown. During cover downloads, product counters stay stable and the progress label changes to covers. Use native controls, visible labels, keyboard focus, and readable narrow layouts.

## Implementer notes

Treat docs/SPEC.md as the behavioral authority. Use frontend state snapshots emitted by Go at no more than 4 Hz; the demo JavaScript is disposable. Avoid per-product DOM updates and loading complete JSON into the frontend. Local storage is ready by default; the data-folder chooser can be a small idle-only settings action later.

The implemented Wails screen lives in `frontend/`. It uses Go snapshots at 4 Hz,
skips unchanged snapshots, masks and clears pasted cookies, and pages persisted
errors 50 at a time. Folder selection is implemented in Local data and requires
ending any unfinished run first. The standalone mockup stays unchanged and does
not import credentials or crawl. Native Windows checks covered desktop/narrow
layouts and keyboard focus; the production renderer is tested across every run
state. Linux light appearance was checked under WSLg with software rendering.

There is intentionally no navigation to other storefronts, browsing, analysis, remote storage, or infrastructure settings. No external assets or icon package are required by this mockup.
