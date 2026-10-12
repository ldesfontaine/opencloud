// Copie un texte et dit si c'est fait. Hors HTTPS, sur une adresse du
// réseau local, le navigateur n'offre pas navigator.clipboard : on passe
// alors par une zone de texte et la commande de copie.
export function copyText(text: string): Promise<boolean> {
  if (!("clipboard" in navigator)) {
    return Promise.resolve(copyByCommand(text));
  }
  return navigator.clipboard.writeText(text).then(
    () => true,
    () => copyByCommand(text),
  );
}

function copyByCommand(text: string): boolean {
  const area = document.createElement("textarea");
  area.value = text;
  area.setAttribute("readonly", "");
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.appendChild(area);
  area.select();
  let done = false;
  try {
    done = document.execCommand("copy");
  } catch {
    done = false;
  }
  area.remove();
  return done;
}
