import test from "node:test";
import assert from "node:assert/strict";
import { controls, progress, labels } from "./model.js";
test("connection and run transitions gate actions", () => {
  const ready = { storageReady: true, connectionReady: true };
  assert.equal(
    controls({ ...ready, connectionReady: false }).primaryDisabled,
    true,
  );
  assert.equal(
    controls({
      ...ready,
      state: "fetching",
      running: true,
      run: { phase: "fetch" },
    }).primary,
    "Pause crawl",
  );
  assert.equal(
    controls({ ...ready, state: "paused", run: { phase: "fetch" } }).primary,
    "Resume crawl",
  );
  assert.equal(
    controls({ ...ready, connectionReady: false, run: { phase: "covers" } })
      .primaryDisabled,
    false,
  );
  assert.equal(
    controls({ ...ready, run: { phase: "fetch" } }).retryDisabled,
    true,
  );
  assert.equal(
    controls({ ...ready, state: "pausing", running: true }).primaryDisabled,
    true,
  );
});
test("discovery is indeterminate and refresh progress is separate from catalog", () => {
  assert.equal(
    progress({ run: {}, phase: "discover", found: 50 }).indeterminate,
    true,
  );
  const p = progress({
    phase: "fetch",
    saved: 20,
    eligible: 100,
    catalogued: 500,
    productErrors: 3,
  });
  assert.equal(p.value, 23);
  assert.equal(p.max, 100);
  assert.match(p.count, /20/);
  assert.equal(
    progress({ phase: "covers", running: true, coverScanDone: false })
      .indeterminate,
    true,
  );
});
test("cover progress distinguishes the saved catalog from this scan", () => {
  const paused = { phase: "covers", state: "paused", cataloguedCovers: 80000 };
  const inventory = `${(80000).toLocaleString()} covers saved locally`;
  assert.equal(progress(paused).count, inventory);
  assert.equal(progress({ ...paused, running: true, coverScanned: 17 }).count,
    `${inventory} · 17 products checked`);
  assert.equal(progress({ ...paused, state: "completed_errors", coverScanDone: true,
    coverSaved: 4, coverTotal: 5, coverErrors: 1 }).count,
    `${inventory} · 4 / 5 downloaded this pass · 1 failed`);
});

test("all production states have useful labels", () => {
  for (const state of [
    "setup",
    "ready",
    "starting",
    "discovering",
    "fetching",
    "covers",
    "pausing",
    "paused",
    "completed",
    "completed_errors",
    "blocked_auth",
    "blocked_storage",
    "ended",
  ])
    assert.ok(labels[state]);
});
