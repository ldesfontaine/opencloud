import { api, segment } from "./client";
import type { RoleID } from "./machines";
import { route } from "./routes";

// La session du navigateur : version, langue, et les seuils que le serveur
// tient pour que les écrans comptent comme lui. Lue une fois par i18n, avant
// tout affichage : pas de hook de lecture ici.

export interface CertificateThresholds {
  warning: number;
  danger: number;
}

export type ResourceName = "cpu" | "memory" | "disk";

export interface ResourceThresholds {
  attention: number;
  danger: number;
}

export interface Session {
  version: string;
  // Le commit du binaire ; vide quand il n'est pas construit par make.
  commit: string;
  language: string;
  languages: string[];
  // Les jours à partir desquels un certificat se signale. Le serveur les
  // tient : le compteur et la couleur d'une ligne basculent sur le même
  // chiffre.
  certificate_thresholds: CertificateThresholds;
  // Les pourcentages à partir desquels une ressource alerte, puis s'aggrave,
  // tels que réglés sous Paramètres ; une machine peut porter les siens.
  resource_thresholds: Record<ResourceName, ResourceThresholds>;
  // Les écritures que le registre des verbes ouvre aux jetons d'API : un
  // bouton qui appelle l'une d'elles porte la puce « MCP ».
  verbs: SessionVerb[];
  // Les rôles qu'une machine peut recevoir, avec les droits qu'ils cochent
  // d'avance : le serveur seul écrit ce préréglage.
  roles: SessionRole[];
}

export interface SessionRole {
  id: RoleID;
  capabilities: string[];
}

interface SessionVerb {
  id: string;
  route: string;
}

// Un catalogue : la clé, et son format Go (%s, %d).
export type Catalog = Record<string, string>;

const sessionPath = "/api/session";

export async function fetchSession(): Promise<Session> {
  return api.get<Session>(sessionPath);
}

export async function fetchCatalog(language: string): Promise<Catalog> {
  return api.get<Catalog>(`/api/i18n/${segment(language)}`);
}

export async function saveLanguage(language: string): Promise<void> {
  return api.send(route("PUT /api/session/language"), { language });
}
