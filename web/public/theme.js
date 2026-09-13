// Thème : choix explicite mémorisé par le navigateur, sinon celui du système.
// Chargé avant le corps de page pour ne jamais afficher le mauvais thème.
(function () {
  var root = document.documentElement;
  var system = window.matchMedia("(prefers-color-scheme: dark)");
  function stored() {
    try { return localStorage.getItem("theme"); } catch (_) { return null; }
  }
  function apply() {
    var choice = stored();
    root.dataset.theme = choice === "dark" || choice === "light" ? choice : (system.matches ? "dark" : "light");
  }
  apply();
  system.addEventListener("change", apply);
  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-set-theme]");
    if (!button) { return; }
    try { localStorage.setItem("theme", button.dataset.setTheme); } catch (_) {}
    apply();
  });
})();
