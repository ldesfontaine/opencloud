// Les contrôles que l'assistant fait avant d'envoyer : le serveur reste juge
// (firewall.Definition.Normalize), ceux-ci évitent un aller-retour pour une
// faute évidente.

// Une adresse d'API commence par https:// et nomme un hôte.
export function isHTTPSAddress(raw: string): boolean {
  const address = raw.trim();
  return address.startsWith("https://") && address.length > "https://".length;
}

// Une clé ou un secret d'API : 8 à 200 caractères imprimables, sans espace.
export function isCredential(value: string): boolean {
  return value.length >= 8 && value.length <= 200 && /^[\x21-\x7e]+$/.test(value);
}
