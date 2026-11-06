export function maintenanceUI({ api, getState, invoke }) {
  const $ = id => document.getElementById(id), n = v => Number(v || 0).toLocaleString();
  let selected = "crawl", page = 0, source = "", priorState = "";
  const narrow = window.matchMedia?.("(max-width: 760px)");
  const orientTabs = () => document.querySelector('[role="tablist"]').setAttribute("aria-orientation", narrow?.matches ? "horizontal" : "vertical");
  orientTabs();
  narrow?.addEventListener("change", orientTabs);
  function select(name, focus = true) {
    selected = name;
    for (const tab of ["crawl", "maintenance"]) {
      $(tab + "Tab").setAttribute("aria-selected", String(tab === name));
      $(tab + "Tab").tabIndex = tab === name ? 0 : -1;
      $(tab + "Panel").hidden = tab !== name;
    }
    $("workspaceTitle").textContent = name === "crawl" ? "Crawler" : "Data maintenance";
    $("workspaceContext").textContent = name === "crawl" ? "Collection tools" : "Catalog tools";
    $("workspaceDescription").textContent = name === "crawl"
      ? "Collect product records and covers from your selected storefront."
      : "Keep your local catalog up to date. Original responses are preserved.";
    $("workspace").scrollTop = 0;
    if (focus) $(name + "Tab").focus();
    if (name === "maintenance" && api && getState().storageAvailable && !getState().busy && !getState().maintenance?.running) {
      void invoke(() => api.RefreshMaintenance());
    }
  }
  for (const name of ["crawl", "maintenance"]) {
    $(name + "Tab").onclick = () => select(name);
    $(name + "Tab").onkeydown = event => {
      if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      select(event.key === "Home" ? "crawl" : event.key === "End" ? "maintenance" : selected === "crawl" ? "maintenance" : "crawl");
    };
  }
  $("openMaintenance").onclick = () => select("maintenance");
  async function issues() {
    if (!api) return;
    const requestedSource = getState().source, requestedPage = page;
    try {
      const rows = await api.MaintenanceIssues(page * 50);
      if (requestedSource !== getState().source || requestedPage !== page) return;
      $("metadataIssueList").replaceChildren();
      for (const row of rows || []) {
        const li = document.createElement("li"), title = document.createElement("strong"), detail = document.createElement("span");
        title.textContent = `Product ${row.id} · ${row.status === "failed" ? "Extraction failed" : "Completed with warnings"}`;
        detail.textContent = [row.message, ...(row.warnings || [])].join(" · ");
        li.append(title, detail); $("metadataIssueList").append(li);
      }
      if (!rows?.length) { const li = document.createElement("li"); li.textContent = "No issues on this page."; $("metadataIssueList").append(li); }
      $("metadataPrevious").disabled = page === 0;
      $("metadataNext").disabled = (rows?.length || 0) < 50;
      $("metadataPage").textContent = `Page ${page + 1}`;
    } catch { $("metadataIssueList").textContent = "Could not read metadata issues. Check local storage and refresh."; }
  }
  $("metadataIssues").ontoggle = () => { if ($("metadataIssues").open) void issues(); };
  $("metadataPrevious").onclick = () => { page = Math.max(0, page - 1); return issues(); };
  $("metadataNext").onclick = () => { page++; return issues(); };
  $("reloadMetadataIssues").onclick = issues;
  $("applySchema").onclick = () => invoke(() => api.StartMaintenance("schema"));
  $("extractMetadata").onclick = () => invoke(() => api.StartMaintenance("metadata"));
  $("pauseMaintenance").onclick = () => invoke(() => api.PauseMaintenance());
  $("endMaintenance").onclick = () => invoke(() => api.EndMaintenance());
  $("refreshMaintenance").onclick = () => invoke(() => api.RefreshMaintenance());
  return {
    render(s, busy) {
      const m = s.maintenance || {}, summary = m.summary || {}, job = m.job;
      const crawlHeld = Boolean(s.run || s.running), blocked = busy || !s.storageAvailable || crawlHeld;
      $("maintenanceGate").hidden = !(m.needsSchema || m.running || m.held);
      $("maintenanceGateMessage").textContent = m.needsSchema ? "Database needs maintenance before crawling." : m.running ? "Data maintenance is in progress. Pause and end it before starting a crawl." : "Resume or end data maintenance before crawling.";
      $("schemaStatus").textContent = m.newerSchema ? "This catalog needs a newer version of Nekonomicon." : m.needsSchema ? "A database update is required. Opening the app does not apply it." : "Database structure is up to date.";
      $("applySchema").hidden = !m.needsSchema;
      $("applySchema").disabled = blocked || m.running || m.newerSchema;
      $("maintenanceBlock").hidden = !crawlHeld;
      $("maintenanceBlock").textContent = "End the unfinished crawl before starting maintenance.";
      $("metadataCurrent").textContent = m.needsSchema ? "—" : n(summary.current);
      $("metadataPending").textContent = m.needsSchema ? "—" : n(summary.pending);
      $("metadataCoverage").textContent = m.needsSchema ? "Apply the database update to check saved responses." : `${n(summary.total)} saved responses · ${n(summary.warnings)} with warnings · ${n(summary.failed)} failed extractions`;
      $("extractMetadata").textContent = m.held ? "Resume extraction" : summary.failed && summary.pending === summary.failed ? "Retry failed extractions" : "Extract metadata";
      $("extractMetadata").disabled = blocked || m.running || m.needsSchema || (!m.held && !summary.pending);
      $("pauseMaintenance").hidden = !m.running;
      $("pauseMaintenance").textContent = m.needsSchema ? "Cancel database update" : "Pause";
      $("pauseMaintenance").disabled = busy || m.state === "pausing";
      $("endMaintenance").hidden = !m.held;
      $("endMaintenance").disabled = busy || m.running;
      $("refreshMaintenance").disabled = busy || m.running || m.needsSchema || !s.storageAvailable;
      $("maintenanceState").textContent = ({ ready: "Ready", schema_required: "Database update required", starting: "Preparing maintenance", upgrading: "Updating database structure", extracting: "Extracting product metadata", pausing: "Finishing current work", paused: "Paused", blocked: "Maintenance needs attention", completed: "Maintenance complete", completed_warnings: "Finished with issues", ended: "Maintenance ended" })[m.state] || "Waiting for local storage";
      $("maintenanceMessage").textContent = m.message || "";
      $("maintenanceCount").textContent = job && !m.needsSchema ? `${n(job.processed)} / ${n(job.total)} processed · ${n(job.warned)} with warnings · ${n(job.failed)} failed` : "";
      const p = $("maintenanceProgress"); p.max = Math.max(1, job?.total || 0);
      if (m.running && ["starting", "upgrading"].includes(m.state)) p.removeAttribute("value");
      else p.value = job?.processed || (m.state === "completed" ? 1 : 0);
      if (source !== s.source) { source = s.source; page = 0; $("metadataIssueList").replaceChildren(); if ($("metadataIssues").open) void issues(); }
      if (priorState !== m.state && !m.running && $("metadataIssues").open) void issues();
      priorState = m.state;
    }
  };
}
