import { controls, progress, labels } from "./model.js";
import { openAIUI } from "./openai.js";
import { enrichmentUI } from "./enrichment.js";
import { booksUI } from "./books.js";
import { maintenanceUI } from "./maintenance.js";
const $ = (id) => document.getElementById(id),
  api = window.go?.main?.App;
let abortRequested = false, overlayVisible = true, previousFocus = null;
let state = {},
  profiles = [],
  page = 0,
  lastState = "",
  localBusy = false,
  lastRendered = "";
const openAI = openAIUI({ api, invoke });
const books = booksUI({ api, getState: () => state });
const enrichment = enrichmentUI({api,getState:()=>state,invoke,books});
const maintenance = maintenanceUI({ api, getState: () => state, invoke, onSelect: name => enrichment.activate(name === "dnd") });
function message(text) {
  $("notice").textContent = text;
  $("notice").hidden = !text;
}
function text(id, value) {
  if ($(id).textContent !== value) $(id).textContent = value;
}
function renderSteps(s = state) {
  const mode = s.run?.mode || $("mode").value;
  const covers = s.run ? s.run.covers : $("covers").checked;
  for (const [id, phase, description, included] of [
    ["stepDiscover", "discover", "Find product IDs", !["retry", "covers"].includes(mode)],
    ["stepFetch", "fetch", "Keep full responses", mode !== "covers"],
    ["stepCovers", "covers", "Save primary images", mode === "covers" || covers],
  ]) {
    const step = $(id);
    if ((s.run || s.running) && s.phase === phase) step.setAttribute("aria-current", "step");
    else step.removeAttribute("aria-current");
    step.dataset.skipped = String(!included);
    step.querySelector("div > span").textContent = included ? description : "Not included";
  }
}
function render(s) {
  const serialized = JSON.stringify([s, localBusy, profiles.length, abortRequested]);
  if (serialized === lastRendered) return;
  lastRendered = serialized;
  const sourceChanged = state.source !== s.source;
  const priorLoadingError = state.loadingError;
  state = s;
  maintenance.render(s, s.busy || localBusy || s.enrichment?.running);
  enrichment.render(s);
  books.render(s);
  openAI.render(s, localBusy || s.enrichment?.running);
  const c = controls({ ...s, busy: s.busy || localBusy }),
    p = progress(s);
  $("primary").textContent = c.primary;
  $("primary").disabled = c.primaryDisabled;
  $("end").hidden = c.endHidden;
  $("end").disabled = s.busy || localBusy || s.state === "pausing";
  $("source").disabled = c.locked || s.busy || localBusy;
  $("source").value = s.source || "dtrpg";
  $("websiteCookie").placeholder = s.websiteHost || "www.drivethrurpg.com";
  $("apiCookie").placeholder = s.apiHost || "api.drivethrurpg.com";
  if (sourceChanged) {
    $("testID").value = s.probeID || 584455;
    $("websiteCookie").value = "";
    $("apiCookie").value = "";
    page = 0;
    $("errors").replaceChildren();
    if ($("activity").open) loadErrors();
    if (!s.connectionReady) $("connections").open = true;
  }
  $("mode").disabled = c.locked || s.busy || localBusy;
  $("covers").disabled = c.locked || s.busy || localBusy;
  $("coverOnly").disabled = c.coverDisabled;
  $("chooseFolder").disabled = c.folderDisabled;
  $("retry").disabled = c.retryDisabled;
  for (const id of [
    "profile",
    "container",
    "manual",
    "test",
    "testID",
    "saveCookies",
    "websiteCookie",
    "apiCookie",
  ])
    $(id).disabled = c.connectionDisabled;
  $("import").disabled = c.connectionDisabled || profiles.length === 0;
  if (s.run) {
    if (s.run.mode !== "covers") $("mode").value = s.run.mode;
    $("covers").checked = s.run.covers;
  }
  $("connection").textContent = s.busy
    ? "Checking connection…"
    : s.connectionReady
      ? "Connection ready"
      : "Sign-in / check needed";
  $("connection").dataset.ready = String(Boolean(s.connectionReady && !s.busy));
  text("cataloguedCovers", Number(s.cataloguedCovers || 0).toLocaleString());
  text("runErrors", ((s.productErrors || 0) + (s.coverErrors || 0)).toLocaleString());
  const needsAttention = ["setup", "maintenance_required", "blocked_auth", "blocked_storage", "completed_errors"].includes(s.state) || Boolean(s.metadataWarnings || s.metadataFailures);
  text("runBadge", needsAttention ? "Needs attention" : s.running ? "In progress" : s.state === "paused" ? "Paused" : s.state === "completed" ? "Complete" : s.state === "ready" ? "Ready" : "Idle");
  $("runBadge").dataset.tone = needsAttention ? "warning" : s.running ? "active" : s.state === "completed" ? "success" : "neutral";
  renderSteps(s);
  $("queued").textContent =
    s.phase === "discover" ? "—" : Number(s.queued || 0).toLocaleString();
  $("catalogued").textContent = Number(s.catalogued || 0).toLocaleString();
  $("phase").textContent = labels[s.state] || "Getting ready";
  if (s.state === "completed" && s.metadataWarnings) $("phase").textContent = "Crawl complete with metadata warnings";
  $("count").textContent = p.count;
  $("progress").max = p.max || 1;
  if (p.indeterminate) $("progress").removeAttribute("value");
  else $("progress").value = p.value || 0;
  text("status", s.message || "");
  $("fallback").hidden = !s.sitemapFallback;
  $("sitemapSkipped").hidden = !s.sitemapsSkipped;
  text("sitemapSkipped", `${Number(s.sitemapsSkipped || 0).toLocaleString()} sitemap${s.sitemapsSkipped === 1 ? "" : "s"} skipped by the site's robots policy.`);
  $("speed").textContent =
    s.running && s.phase === "fetch"
      ? `${Number(s.productsPerSecond || 0).toFixed(1)} committed products / second`
      : s.running && s.phase === "covers"
        ? `${Number(s.coversPerSecond || 0).toFixed(1)} covers / second`
        : "";
  text("authNote", s.authNote || "");
  $("folder").textContent = s.dataFolder || "";
  $("storageStatus").textContent = s.storageReady
    ? "Local storage ready"
    : s.storageAvailable ? "Catalog needs maintenance" : "Local storage unavailable";
  $("errorCount").textContent =
    (s.productErrors || 0) + (s.coverErrors || 0) > 0
      ? `· ${(s.productErrors || 0) + (s.coverErrors || 0)} this run`
      : "";
  $("retryNote").textContent = s.run
    ? "End this run before starting a separate retry of failed products."
    : "";
  $("metadataNotice").hidden = !(s.metadataWarnings || s.metadataFailures);
  $("metadataNotice").textContent = `${Number(s.metadataWarnings || 0).toLocaleString()} products have metadata warnings; ${Number(s.metadataFailures || 0).toLocaleString()} need extraction repair. Review Data maintenance. Original responses are saved.`;
  if (
    (s.state === "setup" || s.state === "blocked_auth") &&
    lastState !== s.state
  )
    $("connections").open = true;
  if (!s.storageReady) $("storage").open = true;
  if (lastState !== s.state && $("activity").open) loadErrors();
  lastState = s.state;
  if (s.loadingError !== priorLoadingError && (s.loadingError || priorLoadingError)) message(s.loadingError || "");
  renderLoading(s);
}
function renderLoading(s) {
  const closing = s.closing || abortRequested, visible = s.loading || closing;
  const dialog = $("loadingDialog"), content = $("appContent");
  if (visible && !overlayVisible) previousFocus = document.activeElement;
  $("loadingOverlay").hidden = !visible;
  content.toggleAttribute("inert", Boolean(visible));
  content.setAttribute("aria-busy", String(Boolean(visible)));
  text("loadingTitle", closing ? "Closing Nekonomicon" : s.migrating ? "Upgrading your catalog" : "Opening your catalog");
  text("loadingMessage", closing ? "Waiting for database work to stop safely…" : s.loadingMessage || "Preparing local storage…");
  text("loadingHint", closing ? "The app will close after local storage is safely closed. This may take a moment." : "Large catalogs can take a while. Abort and close cancels unfinished work and exits the app.");
  $("abortLoading").disabled = Boolean(closing);
  $("abortLoading").textContent = closing ? "Closing…" : "Abort and close";
  if (visible && (!dialog.contains(document.activeElement) || document.activeElement?.disabled)) {
    (closing ? dialog : $("abortLoading")).focus();
  } else if (!visible && overlayVisible) {
    const target = previousFocus?.isConnected && !previousFocus.disabled ? previousFocus : $("primary").disabled ? $("chooseFolder") : $("primary");
    target.focus();
    previousFocus = null;
  }
  overlayVisible = Boolean(visible);
}
document.addEventListener("keydown", (event) => {
  if (!overlayVisible) return;
  if (event.key === "Escape") event.preventDefault();
  if (event.key === "Tab") {
    event.preventDefault();
    ($("abortLoading").disabled ? $("loadingDialog") : $("abortLoading")).focus();
  }
});
async function invoke(action) {
  if (localBusy) return;
  message("");
  localBusy = true;
  render(state);
  try {
    await action();
  } catch (err) {
    message(String(err));
  } finally {
    localBusy = false;
    render(await api.Snapshot());
  }
}
async function loadErrors() {
  try {
    const source = state.source, requestedPage = page;
    const items = await api.Failures(page * 50);
    if (source !== state.source || requestedPage !== page) return;
    $("errors").replaceChildren();
    for (const f of items) {
      const li = document.createElement("li"),
        title = document.createElement("strong"),
        detail = document.createElement("span");
      title.textContent = `Product ${f.id} · ${f.phase === "cover" ? "Cover" : "Product"} · ${f.kind}${f.status ? ` · HTTP ${f.status}` : ""}`;
      detail.textContent = f.message;
      li.append(title, detail);
      $("errors").append(li);
    }
    if (!items.length) {
      const li = document.createElement("li");
      li.textContent = "No recorded failures on this page.";
      $("errors").append(li);
    }
    $("previous").disabled = page === 0;
    $("next").disabled = items.length < 50;
    $("pageLabel").textContent = `Page ${page + 1}`;
  } catch {
    message("Could not read recorded failures. Check local storage.");
  }
}
function containers() {
  const p = profiles[Number($("profile").value)];
  $("container").replaceChildren();
  for (const c of p?.containers || [
    { id: 0, name: "Default (no container)" },
  ]) {
    const o = document.createElement("option");
    o.value = c.id;
    o.textContent = c.name;
    $("container").append(o);
  }
}
if (!api) {
  renderLoading({});
  message(
    "Open this screen in the Nekonomicon desktop application. The static file cannot crawl.",
  );
  document
    .querySelectorAll("button,input,select")
    .forEach((el) => (el.disabled = true));
} else {
  $("mode").onchange = () => renderSteps();
  $("covers").onchange = () => renderSteps();
  $("abortLoading").onclick = async () => {
    if (abortRequested || state.closing) return;
    abortRequested = true;
    render(state);
    try {
      await api.AbortAndClose();
    } catch (err) {
      abortRequested = false;
      render(await api.Snapshot());
      message(String(err));
    }
  };
  $("primary").onclick = () =>
    invoke(() =>
      state.running
        ? api.Pause()
        : state.run
          ? api.Resume()
          : api.Start($("mode").value, $("covers").checked),
    );
  $("source").onchange = async () => {
    const source = $("source").value;
    await invoke(() => api.SelectSource(source));
    if (!$("source").disabled) $("source").focus();
  };
  $("end").onclick = () => invoke(() => api.End());
  $("coverOnly").onclick = () => invoke(() => api.Start("covers", false));
  $("retry").onclick = () =>
    invoke(() => api.Start("retry", $("covers").checked));
  $("chooseFolder").onclick = () => invoke(() => api.ChooseDataFolder());
  $("import").onclick = () =>
    invoke(() =>
      api.ImportFirefox(
        Number($("profile").value),
        Number($("container").value),
      ),
    );
  $("test").onclick = () =>
    invoke(() => api.TestConnection(Number($("testID").value)));
  $("saveCookies").onclick = () =>
    invoke(async () => {
      try {
        await api.SaveCookies($("websiteCookie").value, $("apiCookie").value);
      } finally {
        $("websiteCookie").value = "";
        $("apiCookie").value = "";
      }
    });
  $("manual").onclick = () => {
    const show = $("cookieFields").hidden;
    $("cookieFields").hidden = !show;
    $("manual").setAttribute("aria-expanded", String(show));
    if (show) $("websiteCookie").focus();
  };
  $("profile").onchange = containers;
  $("activity").ontoggle = () => {
    if ($("activity").open) loadErrors();
  };
  $("previous").onclick = () => {
    page = Math.max(0, page - 1);
    loadErrors();
  };
  $("next").onclick = () => {
    page++;
    loadErrors();
  };
  $("reloadErrors").onclick = loadErrors;
  window.runtime.EventsOn("progress", render);
  render(await api.Snapshot());
  profiles = (await api.Profiles()) || [];
  $("profile").replaceChildren();
  if (!profiles.length) {
    const o = document.createElement("option");
    o.textContent = "No Firefox profiles found";
    $("profile").append(o);
  }
  profiles.forEach((p, i) => {
    const o = document.createElement("option");
    o.value = i;
    o.textContent = p.name;
    $("profile").append(o);
  });
  containers();
  render(state);
}
