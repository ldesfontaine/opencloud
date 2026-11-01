import { isHTTPSAddress } from "./firewallForm";

// L'adresse d'une page de l'interface web d'OPNsense, construite depuis
// l'adresse saisie : son origine (https, hôte, port), jamais son chemin ni
// ses identifiants. Null tant que l'adresse n'est pas celle d'un hôte https.
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

// La page des utilisateurs : celle que les trois gestes demandent. Chemin
// relevé dans opnsense/core, étiquette 26.7, Core/Menu/Menu.xml
// (System > Access > Users). Les libellés de menu et de champ, eux, sont dans
// les catalogues (firewall.menu_*, firewall.label_*) : ils suivent la langue
// de l'interface, d'après les traductions officielles (opnsense/lang, fr_FR.po).
export const opnsenseUsersPath = "/ui/auth/user";

// Les privilèges se donnent par le nom du champ « Privileges » : OPNsense ne
// les traduit pas (PrivField les rend tels que les ACL.xml les nomment ; aucun
// n'est dans fr_FR.po). Chaque nom est celui de opnsense/core, étiquettes 26.1
// et 26.7, models/OPNsense/*/ACL/ACL.xml ; docs/projet/22-fonctionnement.md
// dit de quel fichier vient chacun.

// Ce que le relevé lit : version (tableau de bord), interfaces, passerelles,
// règles.
export const opnsenseReadPrivileges = ["Lobby: Dashboard", "Status: Interfaces", "System: Gateways", "Firewall: Rules [new]"];

// Les baux DHCP : un seul, selon le serveur DHCP du pare-feu. ISC DHCP n'est
// plus dans le cœur depuis 26.1 (greffon os-isc-dhcp) : il n'est pas proposé.
export const opnsenseDhcpPrivileges = ["Services: Dnsmasq DNS/DHCP: Settings", "Services: DHCP: Kea(v4)"];
