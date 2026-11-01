// Une clé ou un identifiant de jeton ne s'affiche jamais en entier : ses
// quatre premiers caractères suffisent à la reconnaître. Même règle que le
// serveur (httpx.MaskCredential) pour ce qu'il relit.
const visibleCharacters = 4;

export function maskCredential(value: string): string {
  const characters = Array.from(value);
  if (characters.length <= visibleCharacters) {
    return "…";
  }
  return `${characters.slice(0, visibleCharacters).join("")}…`;
}
