export function openAIUI({api, invoke}) {
  const $ = id => document.getElementById(id);
  let signature = "", selected = false, lastActive = "";
  $("openAIAccount").onchange = () => { selected = true; };
  $("openAISignIn").onclick = () => invoke(() => api.SignInOpenAI($("openAIAccount").value));
  $("openAICancel").onclick = () => invoke(() => api.CancelOpenAISignIn());
  $("openAISignOut").onclick = () => invoke(() => api.SignOutOpenAI());
  $("openAISettings").onclick = () => invoke(() => api.OpenAISettings());
  return {render(state, busy) {
    const v = state.openAI || {}, accounts = v.accounts || [], key = JSON.stringify([accounts, v.active]);
    if (signature !== key) {
      const previous = $("openAIAccount").value;
      $("openAIAccount").replaceChildren();
      for (const account of accounts) {
        const option = document.createElement("option"); option.value = account.id; option.textContent = account.label;
        $("openAIAccount").append(option);
      }
      const add = document.createElement("option"); add.value = ""; add.textContent = accounts.length ? "Add another account" : "New ChatGPT account";
      $("openAIAccount").append(add);
      $("openAIAccount").value = selected && lastActive === (v.active || "") && (previous === "" || accounts.some(a => a.id === previous)) ? previous : v.active || accounts[0]?.id || "";
      lastActive = v.active || "";
      signature = key;
    }
    $("openAIStatus").textContent = v.message || "Connect OpenAI for D&D adventure enrichment.";
    $("openAIConnectionLabel").textContent = v.connected ? (v.planEnabled ? "Connected" : "Permission needed") : "Not connected";
    $("openAIAccount").disabled = !api || busy || v.running || state.closing;
    $("openAISignIn").disabled = !api || busy || v.running || state.closing;
    $("openAICancel").hidden = v.state !== "signing_in" || !v.running;
    $("openAICancel").disabled = busy || state.closing;
    $("openAISignOut").hidden = !v.connected;
    $("openAISignOut").disabled = busy || v.running || state.closing;
    $("openAISettings").disabled = !api || state.closing;
  }};
}
