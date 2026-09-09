// Le thème et l'état de la barre latérale, posés sur <html> avant le premier
// rendu : chargé sans defer dans <head>, il évite que la page s'affiche en
// clair puis saute au sombre, ou dépliée puis repliée. Il tourne sur toutes
// les pages, connexion comprise.
(function () {
  const root = document.documentElement;
  const darkQuery = window.matchMedia("(prefers-color-scheme: dark)");

  // Le stockage peut être refusé (navigation privée, réglage du navigateur) :
  // dans ce cas on suit le système et on ne retient rien.
  function remembered(key) {
    try {
      return localStorage.getItem(key);
    } catch (error) {
      return null;
    }
  }

  const theme = remembered("opencloud.theme");
  if (theme === "light" || theme === "dark") {
    root.setAttribute("data-theme", theme);
  } else {
    root.setAttribute("data-theme", darkQuery.matches ? "dark" : "light");
    // Rien de mémorisé : le thème suit le système, même s'il change en cours
    // de route.
    darkQuery.addEventListener("change", function (event) {
      if (remembered("opencloud.theme") === null) {
        root.setAttribute("data-theme", event.matches ? "dark" : "light");
      }
    });
  }

  if (remembered("opencloud.sidebar") === "collapsed") {
    root.setAttribute("data-sidebar", "collapsed");
  }
})();
