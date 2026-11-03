import type { Translate } from "../i18n/context";

// Une clé ou un identifiant de jeton ne s'affiche jamais en entier : ses
// quatre premiers caractères suffisent à la reconnaître. Même règle que le
// serveur (httpx.MaskCredential) pour ce qu'il relit.
const visibleCharacters = 4;

function maskCredential(value: string): string {
  const characters = Array.from(value);
  if (characters.length <= visibleCharacters) {
    return "…";
  }
  return `${characters.slice(0, visibleCharacters).join("")}…`;
}

// Ce que l'écran écrit d'une clé : ses quatre premiers caractères, ou un libellé
// quand il n'y a rien à montrer (valeur pas encore saisie, indice que le serveur
// ne rend pas à un jeton d'API). Jamais un « … » seul.
export function credentialText(t: Translate, value: string): string {
  return value.trim() === "" ? t("credential.not_shown") : maskCredential(value.trim());
}

// L'indice que le serveur a déjà réduit (api_key_hint, token_id_hint) : tel quel,
// ou le libellé s'il est vide.
export function hintText(t: Translate, hint: string): string {
  return hint === "" ? t("credential.not_shown") : hint;
}
