export const labels = {
  maintenance_required: "Database needs maintenance before crawling",
  loading: "Opening local catalog",
  setup: "Sign-in needed",
  ready: "Ready when you are",
  starting: "Preparing your run",
  discovering: "Discovering products",
  fetching: "Fetching products",
  covers: "Downloading covers",
  pausing: "Finishing current requests",
  paused: "Paused",
  completed: "Crawl complete",
  completed_errors: "Finished with errors",
  ended: "Run ended",
  blocked_auth: "Connection needs attention",
  blocked_storage: "Local storage needs attention",
};
export function controls(s) {
  const maintenanceHeld = s.maintenance?.running || s.maintenance?.held || s.enrichment?.running;
  const held = Boolean(s.run),
    locked = held || s.running || Boolean(maintenanceHeld),
    busy = s.busy || s.state === "pausing" || maintenanceHeld;
  const needsConnection =
    (!held || s.run.phase !== "covers") && !s.connectionReady;
  return {
    primary: s.running ? "Pause crawl" : held ? "Resume crawl" : "Start crawl",
    primaryDisabled: busy || !s.storageReady || (!s.running && needsConnection),
    locked,
    connectionDisabled: s.running || s.busy || Boolean(maintenanceHeld),
    coverDisabled: locked || s.busy || !s.storageReady,
    endHidden: !held,
    folderDisabled: locked || s.busy,
    retryDisabled: locked || s.busy || !s.connectionReady || !s.storageReady,
  };
}
export function progress(s) {
  const n = (value) => Number(value || 0).toLocaleString();
  if (s.phase === "discover" && s.run)
    return {
      indeterminate: true,
      count: `${n(s.found)} IDs found · ${n(s.sitemaps)} sitemaps processed`,
    };
  if (s.phase === "covers") {
    const details = [`${n(s.cataloguedCovers)} covers saved locally`];
    if (s.coverScanDone && s.coverTotal)
      details.push(`${n(s.coverSaved)} / ${n(s.coverTotal)} downloaded this pass`);
    else if (s.coverScanned || s.running || s.coverScanDone)
      details.push(`${n(s.coverScanned)} products checked`);
    if (s.coverErrors) details.push(`${n(s.coverErrors)} failed`);
    return {
      indeterminate: !s.coverScanDone && s.running,
      max: Math.max(1, s.coverTotal || 0),
      value: !s.coverTotal && s.coverScanDone ? 1 : (s.coverSaved || 0) + (s.coverErrors || 0),
      count: details.join(" · "),
    };
  }
  if (s.phase === "fetch")
    return {
      max: Math.max(1, s.eligible || 0),
      value: !s.eligible && s.state === "completed" ? 1 : (s.saved || 0) + (s.productErrors || 0),
      count: `${n(s.saved)} / ${n(s.eligible)} saved this run${s.productErrors ? ` · ${n(s.productErrors)} failed` : ""}`,
    };
  return {
    max: 1,
    value: 0,
    count: s.run ? "Run progress retained" : "No active run",
  };
}
