// « Copier » : la commande d'enrôlement fait une seule ligne, on ne la
// sélectionne pas à la main. data-copy porte l'identifiant du champ à prendre.
(function () {
  const buttons = document.querySelectorAll("button[data-copy]");
  const restoreDelay = 1500;

  buttons.forEach(function (button) {
    const field = document.getElementById(button.dataset.copy);
    if (!field) {
      return;
    }

    button.addEventListener("click", function () {
      const said = button.textContent;
      // Le presse-papier demande un contexte sûr ; sinon on sélectionne le
      // texte et l'opérateur fait le geste lui-même.
      if (!navigator.clipboard) {
        field.focus();
        field.select();
        return;
      }
      navigator.clipboard.writeText(field.value).then(function () {
        button.textContent = "Copié";
        setTimeout(function () {
          button.textContent = said;
        }, restoreDelay);
      }, function () {
        field.focus();
        field.select();
      });
    });
  });
})();
