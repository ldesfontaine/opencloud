// Le direct d'une action : les lignes arrivent par SSE, le navigateur reprend
// tout seul après une coupure grâce à Last-Event-ID.
(function () {
  const output = document.getElementById("action-output");
  if (!output || output.dataset.live !== "oui") {
    return;
  }

  const badge = document.getElementById("action-state");
  const notice = document.getElementById("action-live");
  const source = new EventSource(output.dataset.stream);

  // La même coloration que le rendu du serveur : la classe vient du préfixe
  // qu'écrivent les scripts.
  function classOfLine(text) {
    if (text.startsWith("étape:")) {
      return "step";
    }
    if (text.startsWith("avertissement:")) {
      return "warning";
    }
    if (text.startsWith("résultat:")) {
      return "result";
    }
    return "plain";
  }

  source.addEventListener("line", function (event) {
    const text = JSON.parse(event.data).text;
    const line = document.createElement("div");
    line.className = "line " + classOfLine(text);
    line.textContent = text;
    // Le curseur du direct ferme la sortie : les lignes se posent avant lui.
    output.insertBefore(line, notice);
  });

  source.addEventListener("done", function (event) {
    const end = JSON.parse(event.data);
    if (badge) {
      badge.textContent = end.state_label;
      badge.className = "badge " + end.state;
    }
    if (notice) {
      notice.remove();
    }
    source.close();
  });
})();
