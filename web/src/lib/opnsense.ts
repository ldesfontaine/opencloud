import { isHTTPSAddress } from "./firewallForm";

// L'adresse d'une page de l'interface web d'OPNsense, construite depuis
// l'adresse saisie : son origine (https, hôte, port), jamais son chemin ni
// ses identifiants. Null tant que l'adresse n'est pas celle d'un hôte https.
// Chemins relevés dans les sources d'OPNsense 26.1 (Menu.xml), voir
// docs/projet/22-fonctionnement.md.
export function opnsenseLink(address: string, path: string): string | null {
  if (!isHTTPSAddress(address)) {
    return null;
  }
  try {
    return new URL(path, new URL(address.trim()).origin).href;
  } catch {
    return null;
  }
}

// Les gestes à faire dans OPNsense, dans l'ordre. Le chemin est celui de la
// page ; le menu et les privilèges, les libellés qu'OPNsense affiche (en
// anglais, tels quels).
export const opnsenseUsersPath = "/ui/auth/user";

export const opnsenseMenuUsers = "System ‣ Access ‣ Users";

// Ce que le relevé lit : version (tableau de bord), interfaces, passerelles,
// règles. Les baux DHCP dépendent du serveur DHCP du pare-feu.
export const opnsenseReadPrivileges = ["Lobby: Dashboard", "Status: Interfaces", "System: Gateways", "Firewall: Rules [new]"];

// Un seul, selon le serveur DHCP que le pare-feu utilise.
export const opnsenseLeasePrivileges = ["Services: Dnsmasq DNS/DHCP: Settings", "Services: DHCP: Kea(v4)", "Services: ISC DHCPv4: Leases"];
