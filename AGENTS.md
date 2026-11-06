# Working on Nekonomicon

## Source of truth

Read README.md and docs/SPEC.md before implementation. Read docs/design/README.md when changing the UI. The mockup illustrates interaction and visual direction; docs/SPEC.md defines correctness. Report material conflicts instead of silently inventing behavior.

## Scope

- Support DriveThruRPG and DM’s Guild only (DM’s Guild expansion authorized 2026-11-05 after the DTRPG gate passed). Do not add disabled future storefronts or configurable source plugins.
- Use Go and Wails, local SQLite, full API response JSON, and local cover files.
- Keep architecture small: one process, bounded workers and channels, one database writer, three sequential phases.
- Do not restore MongoDB, S3, distributed queues, leases, generic job frameworks, classification, or elaborate product schemas from history.
- Do not port the old crawler wholesale. Port proven auth/header/Firefox behavior and the necessary sitemap-to-API protocol knowledge. Old operational architecture is not authoritative.

## Implementation

- Keep crawling independent of Wails and frontend lifecycles. Use contexts for cancellation and injectable HTTP for tests.
- Preserve complete response bytes, including top-level included resources. Derive catalog metadata through the shared, versioned extractor; never replace the original response with normalized JSON.
- Every data-model extension must ship with both live-crawl handling and an explicit, recoverable Data maintenance path for existing records. Opening a catalog only inspects compatibility: never initialize, migrate, or backfill automatically. Block crawling when schema maintenance is required.
- Extract comprehensively and conservatively: preserve source values, avoid guessed meanings, keep useful fields on partial failures, and retain raw responses on complete extraction failures. Mark older derived data stale.
- Future product displays may assume prices are in euros (user decision 2026-11-06). Extraction preserves original amounts without currency conversion or an invented source currency field.
- Keep one write owner. Prepare statements; use short batch transactions. No network operations inside transactions.
- Bound response size, buffered bytes, worker count, retries, and UI events. Defaults in the spec are tunable starting values, not performance claims.
- Persist success and its run marker together. Never mark buffered results committed. Preserve last good JSON on failed refresh.
- Distinguish product restriction from confirmed session failure. Port the legacy compatibility headers rather than guessing new ones.
- Scope cookies correctly; never print cookie values or commit credentials, Firefox databases, runtime databases, covers, or response dumps from authenticated accounts.
- Do not forward authentication to image CDNs or unapproved redirects. Sanitize remote image paths.
- Keep UI text useful to the user. Avoid surfacing internal queues, SQL settings, or worker mechanics as normal setup tasks.

## Validation

- Use focused tests for sitemap recursion/gzip/deduplication, product-ID mapping, authentication scope, batch rollback, resume after out-of-order completion, and atomic cover replacement.
- Test HTTP behavior with a local test server. Never start a broad live crawl merely to validate a UI change.
- Once Go code exists, run gofmt, go test ./..., and go vet ./... for relevant Go changes. Run frontend checks and a Wails build for integrated UI changes when tooling is available; report unavailable checks explicitly.
- Benchmark storage with realistic response sizes and a temporary local database. Report hardware/environment, dataset, indexes, durability settings, and whether measurements include HTTP and covers. Do not promise an unmeasured records-per-second target.
- Check desktop and narrow layouts, keyboard access, and all run states. Mockup numbers must remain explicitly illustrative.
- Prefer tests of behavior and failure recovery over tests that mirror the implementation. Keep changes focused and explain material limitations.

## Delivery

Build and deliver the Windows application as `build/bin/Nekonomicon.exe`, replacing the primary executable so existing symlinks and shortcuts continue to work. Do not create alternative executable names (such as `Nekonomicon-redesign.exe`, `Nekonomicon-loading.exe`, or version-suffixed names) unless the user explicitly requests one. If the primary executable is locked by a running instance, arrange a safe close before replacing it; do not work around the lock by delivering a differently named executable or forcibly terminating active catalog work.

Keep docs current with implemented behavior. Do not claim the static mockup performs real crawling or authentication. Do not change the agreed architecture or expand scope to other storefronts without an explicit task. Routine implementation choices within this specification do not require repeated approval.
