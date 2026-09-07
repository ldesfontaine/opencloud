// Le menu « Mon compte » est un <details> : il s'ouvre et se ferme sans
// script. Ces quelques lignes ajoutent ce que le HTML seul ne fait pas —
// refermer au clic ailleurs et à la touche Échap.
(function () {
  const menu = document.getElementById("account-menu");
  if (!menu) {
    return;
  }

  document.addEventListener("click", function (event) {
    if (!menu.contains(event.target)) {
      menu.open = false;
    }
  });

  document.addEventListener("keydown", function (event) {
    if (event.key === "Escape") {
      menu.open = false;
    }
  });
})();
