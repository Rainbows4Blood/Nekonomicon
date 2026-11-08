export function maintenanceUI({ api, getState, invoke, onSelect = () => {} }) {
  const $ = id => document.getElementById(id), n = v => Number(v || 0).toLocaleString();
  let selected = "crawl", page = 0, folder = "", priorState = "";
  const issueAction = () => $("issueTask").value;
  const narrow = window.matchMedia?.("(max-width: 760px)");
  const orientTabs = () => document.querySelector('[role="tablist"]').setAttribute("aria-orientation", narrow?.matches ? "horizontal" : "vertical");
  orientTabs();
  narrow?.addEventListener("change", orientTabs);
  function select(name, focus = true) {
    selected = name;
    for (const tab of ["crawl", "maintenance", "dnd"]) {
      $(tab + "Tab").setAttribute("aria-selected", String(tab === name));
      $(tab + "Tab").tabIndex = tab === name ? 0 : -1;
      $(tab + "Panel").hidden = tab !== name;
    }
    $("workspaceTitle").textContent = name === "crawl" ? "Crawler" : name === "dnd" ? "Dungeons & Dragons" : "Data maintenance";
    $("workspaceContext").textContent = name === "crawl" ? "Collection tools" : name === "dnd" ? "The Nekonomicon archives" : "Catalog tools";
    $("workspaceDescription").textContent = name === "crawl"
      ? "Collect product records and covers from your selected storefront."
      : name === "dnd" ? "Tomes, tales & worlds waiting to be discovered." : "Maintain the whole database across both storefronts. Original responses are preserved.";
    document.documentElement.classList.toggle("fantasy", name === "dnd");
    onSelect(name);
    renderScope(getState());
    $("workspace").scrollTop = 0;
    if (focus) $(name + "Tab").focus();
    if (name === "maintenance" && api && getState().storageAvailable && !getState().busy && !getState().maintenance?.running) {
      void invoke(() => api.RefreshMaintenance());
    }
  }
  for (const name of ["crawl", "maintenance", "dnd"]) {
    $(name + "Tab").onclick = () => select(name);
    $(name + "Tab").onkeydown = event => {
      if (!["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown", "Home", "End"].includes(event.key)) return;
      event.preventDefault();
      const tabs = ["crawl", "maintenance", "dnd"], direction = ["ArrowLeft", "ArrowUp"].includes(event.key) ? -1 : 1;
      select(event.key === "Home" ? tabs[0] : event.key === "End" ? tabs.at(-1) : tabs[(tabs.indexOf(name) + direction + tabs.length) % tabs.length]);
    };
  }
  $("bookMaintenance").onclick = () => select("maintenance");
  $("openMaintenance").onclick = () => select("maintenance");
  function renderScope(s) {
    $("footerSource").textContent = selected !== "crawl" ? "All storefronts" : s.source === "dmsguild" ? "DM’s Guild" : "DriveThruRPG";
  }
  async function issues() {
    if (!api) return;
    const requestedFolder = getState().dataFolder, requestedPage = page, requestedAction = issueAction();
    try {
      const rows = await (requestedAction === "catalog" ? api.CatalogIssues(page * 50) : requestedAction === "groups" ? api.GroupIssues(page * 50) : api.MaintenanceIssues(page * 50));
      if (requestedFolder !== getState().dataFolder || requestedPage !== page || requestedAction !== issueAction()) return;
      $("metadataIssueList").replaceChildren();
      for (const row of rows || []) {
        const li = document.createElement("li"), title = document.createElement("strong"), detail = document.createElement("span");
        title.textContent = `${row.source === "dmsguild" ? "DM’s Guild" : "DriveThruRPG"} · Product ${row.id} · ${row.status === "failed" ? "Processing failed" : "Completed with warnings"}`;
        detail.textContent = [row.message, ...(row.warnings || [])].join(" · ");
        li.append(title, detail); $("metadataIssueList").append(li);
      }
      if (!rows?.length) { const li = document.createElement("li"); li.textContent = "No issues on this page."; $("metadataIssueList").append(li); }
      $("metadataPrevious").disabled = page === 0;
      $("metadataNext").disabled = (rows?.length || 0) < 50;
      $("metadataPage").textContent = `Page ${page + 1}`;
    } catch { $("metadataIssueList").textContent = "Could not read maintenance issues. Check local storage and refresh."; }
  }
  $("issueTask").onchange = () => { page = 0; return issues(); };
  $("metadataIssues").ontoggle = () => { if ($("metadataIssues").open) void issues(); };
  $("metadataPrevious").onclick = () => { page = Math.max(0, page - 1); return issues(); };
  $("metadataNext").onclick = () => { page++; return issues(); };
  $("reloadMetadataIssues").onclick = issues;
  $("applySchema").onclick = () => invoke(() => api.StartMaintenance("schema"));
  $("buildCatalog").onclick = () => invoke(() => api.StartMaintenance("catalog"));
  $("buildGroups").onclick = () => invoke(() => api.StartMaintenance("groups"));
  $("extractMetadata").onclick = () => invoke(() => api.StartMaintenance("metadata"));
  $("pauseMaintenance").onclick = () => invoke(() => api.PauseMaintenance());
  $("endMaintenance").onclick = () => invoke(() => api.EndMaintenance());
  $("refreshMaintenance").onclick = () => invoke(() => api.RefreshMaintenance());
  return {
    render(s, busy) {
      renderScope(s);
      const m = s.maintenance || {}, summary = m.summary || {}, g = m.groups || {}, job = m.job;
      const groupsHeld = m.held && m.action === "groups", metadataHeld = m.held && m.action === "metadata", catalogHeld = m.held && m.action === "catalog", catalog = m.catalog || {};
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
      $("extractMetadata").textContent = metadataHeld ? "Resume extraction" : summary.failed && summary.pending === summary.failed ? "Retry failed extractions" : "Extract metadata";
      $("extractMetadata").disabled = blocked || m.running || m.needsSchema || (m.held && !metadataHeld) || (!metadataHeld && !summary.pending);
      $("groupsCurrent").textContent = m.needsSchema ? "—" : n(g.current);
      $("groupsPending").textContent = m.needsSchema ? "—" : n(g.pending);
      $("groupsCoverage").textContent = m.needsSchema ? "Apply the database update to prepare group storage." : `${n(g.total)} saved responses · ${n(g.warnings)} with grouping warnings · ${n(g.failed)} failed`;
      $("buildGroups").textContent = groupsHeld ? "Resume grouping" : "Build D&D groups";
      $("buildGroups").disabled = blocked || m.running || m.needsSchema || (m.held && !groupsHeld) || (!groupsHeld && !g.pending);
      $("catalogCurrent").textContent = m.needsSchema ? "—" : n(catalog.current);
      $("catalogPending").textContent = m.needsSchema ? "—" : n(catalog.pending);
      $("catalogCoverage").textContent = m.needsSchema ? "Apply the database update to prepare the browsing indexes." : `${n(catalog.total)} saved responses · ${n(catalog.warnings)} with warnings · ${n(catalog.failed)} failed`;
      $("buildCatalog").textContent = catalogHeld ? "Resume indexing" : "Build D&D browsing index";
      $("buildCatalog").disabled = blocked || m.running || m.needsSchema || (m.held && !catalogHeld) || (!catalogHeld && !catalog.pending);
      $("pauseMaintenance").hidden = !m.running;
      $("pauseMaintenance").textContent = m.needsSchema ? "Cancel database update" : "Pause";
      $("pauseMaintenance").disabled = busy || m.state === "pausing";
      $("endMaintenance").hidden = !m.held;
      $("endMaintenance").disabled = busy || m.running;
      $("refreshMaintenance").disabled = busy || m.running || m.needsSchema || !s.storageAvailable;
      $("maintenanceState").textContent = ({ ready: "Ready", schema_required: "Database update required", starting: "Preparing maintenance", upgrading: "Updating database structure", extracting: "Extracting product metadata", grouping: "Building D&D groups", indexing: "Indexing the D&D library", pausing: "Finishing current work", paused: "Paused", blocked: "Maintenance needs attention", completed: "Maintenance complete", completed_warnings: "Finished with issues", ended: "Maintenance ended" })[m.state] || "Waiting for local storage";
      $("maintenanceMessage").textContent = m.message || "";
      $("maintenanceCountsWarning").hidden = !m.countsStale;
      $("maintenanceCount").textContent = job && !m.needsSchema ? `${n(job.processed)} / ${n(job.total)} processed · ${n(job.warned)} with warnings · ${n(job.failed)} failed` : "";
      const p = $("maintenanceProgress"); p.max = Math.max(1, job?.total || 0);
      if (m.running && ["starting", "upgrading"].includes(m.state)) p.removeAttribute("value");
      else p.value = job?.processed || (m.state === "completed" ? 1 : 0);
      if (folder !== s.dataFolder) { folder = s.dataFolder; page = 0; $("metadataIssueList").replaceChildren(); if ($("metadataIssues").open) void issues(); }
      if (priorState !== m.state && !m.running && $("metadataIssues").open) void issues();
      priorState = m.state;
    }
  };
}
