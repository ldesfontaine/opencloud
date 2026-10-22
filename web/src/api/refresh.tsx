import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

// Les sujets du direct, tels que le serveur les nomme. Ajouter un sujet,
// c'est ajouter son nom ici, puis sa ligne dans initialVersions (le
// compilateur la réclame) : l'écoute du direct suit seule.
// Le type Topic se lit dans la liste.
export const topics = ["machines", "jobs", "resources", "containers", "probes", "status", "alerts", "directory", "actions", "backups"] as const;

export type Topic = (typeof topics)[number];

export type Versions = Record<Topic, number>;

// Un littéral typé : un sujet ajouté à topics et oublié ici ne compile pas.
export function initialVersions(): Versions {
  return { machines: 0, jobs: 0, resources: 0, containers: 0, probes: 0, status: 0, alerts: 0, directory: 0, actions: 0, backups: 0 };
}

// Le compteur du sujet demandé avance d'un cran, ou tous sans argument.
export function bumpVersions(versions: Versions, topic?: Topic): Versions {
  const next = { ...versions };
  for (const candidate of topics) {
    if (topic === undefined || topic === candidate) {
      next[candidate] = versions[candidate] + 1;
    }
  }
  return next;
}

// Les sujets qui changent un compteur de /api/counts : tous sauf les
// mesures. L'annuaire en est : la politique d'une unité change le compte
// des mises à jour ; le statut aussi : les incidents ouverts.
export const countedTopics: readonly Topic[] = ["machines", "jobs", "containers", "probes", "status", "alerts", "directory"];

interface Refresh {
  // Relit un sujet, ou tout sans argument.
  refresh: (topic?: Topic) => void;
  // Change quand une ressource de ce chemin doit être relue.
  versionFor: (path: string) => number;
}

const RefreshContext = createContext<Refresh>({ refresh: () => {}, versionFor: () => 0 });

// Le chemin d'une ressource dit son sujet ; les compteurs relèvent de
// tout ce qui se compte. Un chemin sans sujet ne se relit jamais par le
// direct : seulement à la demande de la page, ou quand tout se relit
// (reconnexion, action qui touche tout).
export function topicsOf(path: string): readonly Topic[] {
  if (path.startsWith("/api/counts")) {
    return countedTopics;
  }
  // La fin de support publie sur le sujet des conteneurs : sa carte, sous
  // Paramètres, dit ce que la dernière lecture a donné.
  if (path.startsWith("/api/settings/lifecycle")) {
    return ["containers"];
  }
  // L'écart croise l'annuaire et les conteneurs : il se relit quand l'un ou
  // l'autre change. La proposition d'adoption, elle, ne suit que l'annuaire :
  // elle se lit pour remplir un formulaire, et un rapport de l'agent ne doit
  // pas la relire pendant que l'opérateur le remplit.
  if (path.startsWith("/api/directory/gaps")) {
    return ["directory", "containers"];
  }
  // L'annuaire a son propre sujet, distinct de celui des conteneurs ; le
  // journal des fichiers appliqués le suit : un passage le touche.
  if (path.startsWith("/api/directory") || path.startsWith("/api/manifest")) {
    return ["directory"];
  }
  // La fiche d'un conteneur dit aussi de quelle unité elle relève : elle suit
  // l'annuaire autant que les machines. Seule la fiche, pas ses sous-chemins.
  if (/^\/api\/containers\/[^/?]+(\?.*)?$/.test(path)) {
    return ["containers", "directory"];
  }
  if (path.startsWith("/api/status")) {
    return ["status"];
  }
  // L'état de l'envoi du journal suit les canaux : en cocher un le change.
  if (path.startsWith("/api/alerts") || path.startsWith("/api/audit/forwarding")) {
    return ["alerts"];
  }
  // Les ordres et leur état ; la clé que l'agent a épinglée se lit sur la
  // machine.
  if (path.startsWith("/api/actions")) {
    return ["actions"];
  }
  // L'état d'une sauvegarde est celui de son dernier ordre : la page suit
  // les deux sujets.
  if (path.startsWith("/api/backups")) {
    return ["backups", "actions"];
  }
  if (path.includes("/resources")) {
    return ["resources"];
  }
  if (path.includes("/probes")) {
    return ["probes"];
  }
  if (path.includes("/containers") || path.includes("/network")) {
    return ["containers"];
  }
  // L'offre de mise à jour d'un agent suit la machine (sa version, son état)
  // et les ordres (celui qui est parti).
  if (/^\/api\/machines\/[^/?]+\/agent-update/.test(path)) {
    return ["machines", "actions"];
  }
  if (path.startsWith("/api/machines")) {
    return ["machines"];
  }
  if (path.startsWith("/api/jobs")) {
    return ["jobs"];
  }
  // Un chemin inconnu (l'accès, les réglages, le MCP) n'a pas de sujet.
  return [];
}

// La version d'un chemin : la somme de ses sujets, plus les relectures de
// tout. Elle change quand la ressource doit être relue.
export function versionOf(versions: Versions, everything: number, path: string): number {
  return topicsOf(path).reduce((sum, topic) => sum + versions[topic], everything);
}

// Un seul signal pour relire : après une action de l'opérateur, ou à
// l'arrivée d'un sujet du direct. Chaque sujet a son compteur, pour que la
// page des tâches ne relise pas quand une machine signale.
export function RefreshProvider({ children }: { children: ReactNode }) {
  const [versions, setVersions] = useState<Versions>(initialVersions());
  // Les relectures de tout : elles touchent aussi les chemins sans sujet.
  const [everything, setEverything] = useState(0);
  const refresh = useCallback((topic?: Topic) => {
    setVersions((current) => bumpVersions(current, topic));
    if (topic === undefined) {
      setEverything((current) => current + 1);
    }
  }, []);
  const value = useMemo<Refresh>(
    () => ({
      refresh,
      versionFor: (path) => versionOf(versions, everything, path),
    }),
    [versions, everything, refresh],
  );
  return <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>;
}

export function useRefresh(): Refresh {
  return useContext(RefreshContext);
}
