import test from "node:test";
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { parseHTML } from "linkedom";
import { labels } from "./model.js";

test("actual renderer handles all run states and committed progress", async () => {
  const { window, document } = parseHTML(await readFile(new URL("index.html", import.meta.url), "utf8"));
  globalThis.window = window;
  globalThis.document = document;
  // linkedom has a read-only select.value; browsers supply this native setter.
  for (const select of document.querySelectorAll("select")) {
    Object.defineProperty(select, "value", { configurable: true, get() { return this.querySelector("option[selected]")?.value ?? this.querySelector("option")?.value ?? ""; }, set(value) { for (const option of this.querySelectorAll("option")) option.selected = option.value === String(value); } });
  }
  const initial = { state: "ready", storageReady: true, connectionReady: true, catalogued: 40, authNote: "Checked", dataFolder: "fixture" };
  let event, rows = [];
  window.go = { main: { App: { Snapshot: async () => initial, Profiles: async () => [], Failures: async () => rows } } };
  window.runtime = { EventsOn: (name, callback) => { assert.equal(name, "progress"); event = callback; } };
  await import("./app.js?renderer-test");
  const $ = id => document.getElementById(id);
  for (const state of Object.keys(labels)) {
    const running = ["starting", "discovering", "fetching", "covers", "pausing"].includes(state);
    const held = running || ["paused", "blocked_auth", "blocked_storage"].includes(state);
    const phase = state === "discovering" ? "discover" : state === "covers" ? "covers" : "fetch";
    event({ ...initial, state, running, phase, run: held ? { mode: "refresh", phase, covers: true } : null, saved: 4, eligible: 10, productErrors: 1, queued: 2, coverSaved: 2, coverTotal: 3, coverScanDone: false, connectionReady: state !== "setup" && state !== "blocked_auth", message: "Fixture outcome" });
    assert.equal($("phase").textContent, labels[state]);
    assert.equal($("catalogued").textContent, "40");
    assert.equal($("mode").disabled, held);
    if (state === "discovering") assert.equal($("progress").hasAttribute("value"), false);
    if (state === "paused") assert.equal($("primary").textContent, "Resume crawl");
    if (state === "blocked_auth") assert.equal($("primary").disabled, true);
    if (state === "pausing") assert.equal($("primary").disabled, true);
    if (state === "fetching") assert.match($("count").textContent, /4 \/ 10 saved this run · 1 failed/);
  }
  rows = [{ id: 7, phase: "product", kind: "schema", status: 200, message: "<img src=x onerror=alert(1)>" }];
  $("activity").open = true;
  await $("reloadErrors").onclick();
  assert.equal($("errors").querySelector("img"), null);
  assert.match($("errors").textContent, /onerror/);
  delete globalThis.window;
  delete globalThis.document;
});
