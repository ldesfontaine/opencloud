import { useResource, type Resource } from "../hooks/useResource";
import { segment } from "./client";

// Les clés SSH autorisées que les agents relèvent, en lecture seule. Sujet du
// direct : « machines », que topicsOf devine du chemin.

export type SSHState = "read" | "none" | "denied" | "unsafe" | "too_large";

// Une clé : jamais le contenu du fichier, seulement son empreinte, son type,
// un commentaire borné et les noms d'options reconnus.
export interface SSHKey {
  file: string;
  fingerprint: string;
  type: string;
  bits: number;
  comment: string;
  options: string[];
}

export interface SSHAccount {
  name: string;
  uid: number;
  state: SSHState;
  // Les clés au-delà de la borne n'ont pas été gardées.
  truncated: boolean;
  // Les lignes écartées (invalides, type inconnu).
  ignored: number;
  keys: SSHKey[];
}

// Le relevé d'une machine. reported_at est null tant que l'agent n'a rien dit.
export interface SSHReport {
  machine_id: string;
  machine_name: string;
  reported_at: string | null;
  truncated: boolean;
  accounts: SSHAccount[];
}

export interface SSHMatrixResponse {
  machines: SSHReport[];
}

export function useMachineSSHKeys(machineID: string): Resource<SSHReport> {
  return useResource<SSHReport>(() => `/api/machines/${segment(machineID)}/ssh-keys`);
}

export function useSSHKeyMatrix(): Resource<SSHMatrixResponse> {
  return useResource<SSHMatrixResponse>("/api/ssh-keys");
}
