// Ce que la barre latérale fait et que le HTML seul ne fait pas : replier la
// barre en rail, basculer le thème, et refermer le menu du compte au clic
// ailleurs ou à Échap. L'état retenu est posé sur <html> par appearance.js,
// avant le premier rendu ; ici on ne fait que le changer.
(function () {
  const root = document.documentElement;

  // hx-boost remplace le corps de la page sans recharger : le script peut
  // être rejoué. Les écouteurs ne se posent qu'une fois.
  if (root.dataset.sidebarReady === "oui") {
    return;
  }
  root.dataset.sidebarReady = "oui";

  function remember(key, value) {
    try {
      localStorage.setItem(key, value);
    } catch (error) {
      // Stockage refusé : le geste vaut pour cette page, pas au-delà.
    }
  }

  function isCollapsed() {
    return root.getAttribute("data-sidebar") === "collapsed";
  }

  function isDark() {
    return root.getAttribute("data-theme") === "dark";
  }

  // Le bouton dit ce qu'il fera au prochain clic ; l'interrupteur dit l'état
  // qu'il porte.
  function describeControls() {
    const toggle = document.getElementById("sidebar-toggle");
    if (toggle) {
      const said = isCollapsed() ? "Déplier la barre" : "Replier la barre";
      toggle.title = said;
      toggle.setAttribute("aria-label", said);
    }

    const themeSwitch = document.getElementById("theme-switch");
    if (themeSwitch) {
      themeSwitch.setAttribute("aria-checked", isDark() ? "true" : "false");
    }
  }

  function collapseSidebar(collapsed) {
    if (collapsed) {
      root.setAttribute("data-sidebar", "collapsed");
    } else {
      root.removeAttribute("data-sidebar");
    }
    remember("opencloud.sidebar", collapsed ? "collapsed" : "expanded");
    describeControls();
  }

  function applyTheme(theme) {
    root.setAttribute("data-theme", theme);
    remember("opencloud.theme", theme);
    describeControls();
  }

  // Les clics passent par le document : la barre est remplacée à chaque
  // navigation htmx, les écouteurs, eux, restent.
  document.addEventListener("click", function (event) {
    const target = event.target;

    if (target.closest("#sidebar-toggle")) {
      collapseSidebar(!isCollapsed());
      return;
    }
    if (target.closest("#theme-switch")) {
      applyTheme(isDark() ? "light" : "dark");
      return;
    }

    const menu = document.getElementById("account-menu");
    if (menu && !menu.contains(target)) {
      menu.open = false;
    }
  });

  document.addEventListener("keydown", function (event) {
    if (event.key !== "Escape") {
      return;
    }
    const menu = document.getElementById("account-menu");
    if (menu) {
      menu.open = false;
    }
  });

  // Au chargement, puis après chaque navigation htmx : la barre rendue par le
  // serveur ignore l'état retenu, on le lui redit.
  describeControls();
  document.addEventListener("htmx:load", describeControls);
})();
