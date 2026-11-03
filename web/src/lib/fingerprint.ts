// L'empreinte SHA-256 d'un certificat : le serveur la rend en hexadécimal nu,
// l'opérateur la lit (et la compare à celle d'OPNsense ou de Proxmox) par
// paires séparées de deux-points.

export function formatFingerprint(hex: string): string {
  return (hex.toUpperCase().match(/.{1,2}/g) ?? []).join(":");
}

// Ce que le serveur garde d'une empreinte saisie : minuscules, sans deux-points.
// Assez pour comparer une saisie à celle que le serveur a relevée ; le serveur
// reste juge de la forme (normalizePin).
export function sameFingerprint(entered: string, hex: string): boolean {
  if (hex === "") {
    return false;
  }
  return entered.trim().toLowerCase().replaceAll(":", "") === hex.toLowerCase();
}

// L'empreinte telle qu'on la montre dans un récapitulatif : par paires si c'est
// bien un SHA-256, telle quelle sinon (une saisie que le serveur refusera).
export function displayPin(pin: string): string {
  const hex = pin.trim().toLowerCase().replaceAll(":", "");
  return /^[0-9a-f]{64}$/.test(hex) ? formatFingerprint(hex) : pin.trim();
}
