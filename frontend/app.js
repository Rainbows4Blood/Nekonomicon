import { controls, progress, labels } from "./model.js";
const $ = (id) => document.getElementById(id),
  api = window.go?.main?.App;
let state = {},
  profiles = [],
  page = 0,
  lastState = "",
  localBusy = false,
  lastRendered = "";
function message(text) {
  $("notice").textContent = text;
  $("notice").hidden = !text;
}
function text(id, value) {
  if ($(id).textContent !== value) $(id).textContent = value;
}
function render(s) {
  const serialized = JSON.stringify([s, localBusy, profiles.length]);
  if (serialized === lastRendered) return;
  lastRendered = serialized;
  state = s;
  const c = controls({ ...s, busy: s.busy || localBusy }),
    p = progress(s);
  $("primary").textContent = c.primary;
  $("primary").disabled = c.primaryDisabled;
  $("end").hidden = c.endHidden;
  $("end").disabled = s.busy || localBusy || s.state === "pausing";
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
  $("queued").textContent =
    s.phase === "discover" ? "—" : Number(s.queued || 0).toLocaleString();
  $("catalogued").textContent = Number(s.catalogued || 0).toLocaleString();
  $("phase").textContent = labels[s.state] || "Getting ready";
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
    ? "SQLite + local covers · storage ready"
    : "Local storage unavailable";
  $("errorCount").textContent =
    (s.productErrors || 0) + (s.coverErrors || 0) > 0
      ? `· ${(s.productErrors || 0) + (s.coverErrors || 0)} this run`
      : "";
  $("retryNote").textContent = s.run
    ? "End this run before starting a separate retry of failed products."
    : "";
  if (
    (s.state === "setup" || s.state === "blocked_auth") &&
    lastState !== s.state
  )
    $("connections").open = true;
  if (!s.storageReady) $("storage").open = true;
  if (lastState !== s.state && $("activity").open) loadErrors();
  lastState = s.state;
}
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
    const items = await api.Failures(page * 50);
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
  message(
    "Open this screen in the Nekonomicon desktop application. The static file cannot crawl.",
  );
  document
    .querySelectorAll("button,input,select")
    .forEach((el) => (el.disabled = true));
} else {
  $("primary").onclick = () =>
    invoke(() =>
      state.running
        ? api.Pause()
        : state.run
          ? api.Resume()
          : api.Start($("mode").value, $("covers").checked),
    );
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
