import { segment } from "./client";

// Les écritures que le front envoie, une ligne chacune, telles que le serveur
// les enregistre (internal/server/testdata/routes.txt). C'est ce qui lie un
// geste d'écran à une route d'API, donc à un verbe quand le registre
// internal/verb en porte un : parity.test.ts compare cette liste aux routes
// du serveur et à ses verbes, parity_test.go (Go) refuse une route
// d'écriture que ni cette liste ni sa liste « sans écran » ne nomme.
export const writes = [
  "PUT /api/session/language",
  "POST /api/auth/actions/enable",
  "POST /api/auth/actions/disable",
  "PUT /api/auth/password",
  "PUT /api/settings/disk",
  "PUT /api/settings/mail",
  "POST /api/settings/mail/actions/test",
  "PUT /api/settings/lifecycle",
  "POST /api/settings/api-tokens",
  "PUT /api/settings/api-tokens/{id}/reach",
  "DELETE /api/settings/api-tokens/{id}",
  "POST /api/machines/tokens",
  "DELETE /api/machines/tokens/{id}",
  "POST /api/machines/local-enrollment",
  "DELETE /api/machines/{id}",
  "POST /api/machines/{id}/actions/reenroll",
  "POST /api/machines/{id}/actions/ping",
  "POST /api/machines/{id}/actions/update-agent",
  "POST /api/machines/{id}/actions/rollback-agent",
  "POST /api/machines/{id}/actions/check-updates",
  "PUT /api/machines/{id}/update-last",
  "PUT /api/machines/{id}/role",
  "PUT /api/machines/{id}/placement",
  "PUT /api/machines/{id}/proxmox",
  "POST /api/machines/{id}/actions/remove-proxmox",
  "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/start",
  "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/shutdown",
  "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot",
  "POST /api/machines/{id}/proxmox/guests/{vmid}/actions/snapshot-delete",
  "POST /api/certificate/inspect",
  "POST /api/firewalls",
  "PUT /api/firewalls/{id}",
  "DELETE /api/firewalls/{id}",
  "POST /api/directory/services",
  "PUT /api/directory/services/{id}",
  "DELETE /api/directory/services/{id}",
  "POST /api/directory/services/{id}/actions/deploy",
  "POST /api/directory/services/{id}/actions/redeploy",
  "PUT /api/directory/units/{id}/update-policy",
  "POST /api/directory/units/{id}/actions/upgrade",
  "POST /api/directory/projects",
  "PUT /api/directory/projects/{id}",
  "DELETE /api/directory/projects/{id}",
  "POST /api/directory/ignored",
  "DELETE /api/directory/ignored",
  "POST /api/probes",
  "PUT /api/probes/{id}",
  "DELETE /api/probes/{id}",
  "POST /api/probes/{id}/actions/pause",
  "POST /api/probes/{id}/actions/resume",
  "POST /api/jobs",
  "PUT /api/jobs/{id}",
  "DELETE /api/jobs/{id}",
  "POST /api/jobs/{id}/actions/pause",
  "POST /api/jobs/{id}/actions/resume",
  "POST /api/jobs/{id}/actions/regenerate-token",
  "POST /api/status/components",
  "PUT /api/status/components/{id}",
  "DELETE /api/status/components/{id}",
  "POST /api/status/incidents",
  "PUT /api/status/incidents/{id}",
  "DELETE /api/status/incidents/{id}",
  "POST /api/status/incidents/{id}/updates",
  "POST /api/alerts/actions/acknowledge",
  "POST /api/alerts/{id}/actions/acknowledge",
  "POST /api/alerts/channels",
  "PUT /api/alerts/channels/{id}",
  "DELETE /api/alerts/channels/{id}",
  "POST /api/alerts/channels/{id}/actions/test",
  "POST /api/alerts/mutes",
  "DELETE /api/alerts/mutes/{id}",
  "POST /api/backups/keyring",
  "POST /api/backups/destinations",
  "DELETE /api/backups/destinations/{id}",
  "PUT /api/backups/plans/{subject}",
  "DELETE /api/backups/plans/{subject}",
  "POST /api/backups/plans/{subject}/actions/run",
  "POST /api/verb-requests/{id}/actions/confirm",
  "POST /api/verb-requests/{id}/actions/reject",
] as const;

export type Write = (typeof writes)[number];

// Les noms des segments {…} d'un motif : route() exige exactement ceux-là.
type Names<Pattern extends string> = Pattern extends `${string}{${infer Name}}${infer Rest}` ? Name | Names<Rest> : never;
type Params<Pattern extends Write> = { [Name in Names<Pattern>]: string };

// Les paramètres, puis une requête de filtre si la route en porte (rare :
// ce qui désigne un objet va dans le chemin).
type Rest<Pattern extends Write> = Names<Pattern> extends never
  ? [query?: URLSearchParams]
  : [params: Params<Pattern>, query?: URLSearchParams];

// Une écriture prête à partir : le verbe HTTP et le chemin, jamais écrits à
// la main.
export interface WriteRoute {
  method: "POST" | "PUT" | "DELETE";
  path: string;
  // Le motif dont elle vient, pour la puce « MCP » des boutons (§3.7).
  pattern: Write;
}

// route remplit un motif. Chaque paramètre passe par segment() : rien de ce
// qui change le chemin d'une requête n'entre dans le chemin.
export function route<Pattern extends Write>(pattern: Pattern, ...rest: Rest<Pattern>): WriteRoute {
  const [method, template] = pattern.split(" ") as [WriteRoute["method"], string];
  const [first, second] = rest as [Record<string, string> | URLSearchParams | undefined, URLSearchParams | undefined];
  const query = first instanceof URLSearchParams ? first : second;
  const params = first instanceof URLSearchParams ? {} : (first ?? {});
  const path = template.replace(/\{([a-z]+)\}/g, (_, name: string) => segment(params[name] ?? ""));
  return { method, path: query === undefined ? path : `${path}?${query.toString()}`, pattern };
}
