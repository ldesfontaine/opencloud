// Les quelques lignes que HTMX ne fait pas : copier un texte, suivre un
// sélecteur. Rien d'autre ne vit ici.
(function () {
  document.addEventListener("click", function (event) {
    var button = event.target.closest("[data-copy]");
    if (!button || !navigator.clipboard) { return; }
    navigator.clipboard.writeText(button.dataset.copy).then(function () {
      var label = button.textContent;
      button.textContent = button.dataset.copied || label;
      setTimeout(function () { button.textContent = label; }, 1500);
    });
  });
  document.addEventListener("change", function (event) {
    var select = event.target.closest("select[data-navigate]");
    if (select) { window.location.href = select.value; }
  });
})();
