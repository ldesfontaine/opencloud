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

  source.addEventListener("line", function (event) {
    output.append(JSON.parse(event.data).text + "\n");
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
