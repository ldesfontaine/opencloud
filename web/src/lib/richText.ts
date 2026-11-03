// Un texte du catalogue peut porter des renvois de menu, que le serveur écrit
// en lien Markdown ([Paramètres → E-mails](/parametres#e-mails), voir
// internal/lang/links.go). Seuls les chemins de l'interface passent : une
// adresse extérieure reste du texte.
export type TextPart = { kind: "text"; text: string } | { kind: "link"; label: string; to: string };

const linkPattern = /\[([^\]]+)\]\((\/[^)\s]*)\)/g;

export function splitLinks(text: string): TextPart[] {
  const parts: TextPart[] = [];
  let cursor = 0;
  for (const match of text.matchAll(linkPattern)) {
    const [whole, label = "", to = ""] = match;
    // « //hôte » est une adresse extérieure, pas un chemin de l'interface.
    if (to.startsWith("//")) {
      continue;
    }
    if (match.index > cursor) {
      parts.push({ kind: "text", text: text.slice(cursor, match.index) });
    }
    parts.push({ kind: "link", label, to });
    cursor = match.index + whole.length;
  }
  if (cursor < text.length) {
    parts.push({ kind: "text", text: text.slice(cursor) });
  }
  return parts;
}
