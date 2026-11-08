// Keep startup errors and safe closing available even if a module fails to load.
(() => {
  const button = document.getElementById("abortLoading");
  button.onclick = async () => {
    if (button.disabled) return;
    button.disabled = true;
    button.textContent = "Closing…";
    document.getElementById("loadingMessage").textContent = "Waiting for local storage to close safely…";
    try {
      await window.go.main.App.AbortAndClose();
    } catch {
      button.disabled = false;
      button.textContent = "Abort and close";
      document.getElementById("loadingMessage").textContent = "Could not request closing. Use the window’s close button to exit safely.";
    }
  };
  import("./app.js").catch(() => {
    if (button.disabled) return;
    document.getElementById("loadingOverlay").hidden = false;
    document.getElementById("appContent").inert = true;
    document.querySelector(".loading-spinner").hidden = true;
    document.getElementById("loadingTitle").textContent = "Could not start the interface";
    document.getElementById("loadingMessage").textContent = "The app could not load a required interface file.";
    document.getElementById("loadingHint").textContent = "Close the app and use a freshly built copy of Nekonomicon.";
    button.focus();
  });
})();
