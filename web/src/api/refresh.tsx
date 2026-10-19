import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from "react";

// Les sujets du direct, tels que le serveur les nomme. Ajouter un sujet,
// c'est ajouter son nom ici, puis sa ligne dans initialVersions (le
// compilateur la réclame) : l'écoute du direct suit seule.
// Le type Topic se lit dans la liste.
export const topics = ["machines", "jobs", "resources", "containers", "probes", "status", "alerts", "directory"] as const;

export type Topic = (typeof topics)[number];

export type Versions = Record<Topic, number>;

// Un littéral typé : un sujet ajouté à topics et oublié ici ne compile pas.
export function initialVersions(): Versions {
  return { machines: 0, jobs: 0, resources: 0, containers: 0, probes: 0, status: 0, alerts: 0, directory: 0 };
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

interface Refresh {
  // Relit un sujet, ou tout sans argument.
  refresh: (topic?: Topic) => void;
  // Change quand une ressource de ce chemin doit être relue.
  versionFor: (path: string) => number;
}

const RefreshContext = createContext<Refresh>({ refresh: () => {}, versionFor: () => 0 });

// Le chemin d'une ressource dit son sujet ; les compteurs relèvent de
// tout ce qui se compte.
export function topicsOf(path: string): Topic[] {
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
  if (path.startsWith("/api/alerts")) {
    return ["alerts"];
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
  if (path.startsWith("/api/machines")) {
    return ["machines"];
  }
  if (path.startsWith("/api/jobs")) {
    return ["jobs"];
  }
  // Un chemin inconnu relit tout, sauf les mesures.
  return topics.filter((topic) => topic !== "resources");
}

// Un seul signal pour relire : après une action de l'opérateur, ou à
// l'arrivée d'un sujet du direct. Chaque sujet a son compteur, pour que la
// page des tâches ne relise pas quand une machine signale.
export function RefreshProvider({ children }: { children: ReactNode }) {
  const [versions, setVersions] = useState<Versions>(initialVersions());
  const refresh = useCallback((topic?: Topic) => {
    setVersions((current) => bumpVersions(current, topic));
  }, []);
  const value = useMemo<Refresh>(
    () => ({
      refresh,
      versionFor: (path) => topicsOf(path).reduce((sum, topic) => sum + versions[topic], 0),
    }),
    [versions, refresh],
  );
  return <RefreshContext.Provider value={value}>{children}</RefreshContext.Provider>;
}

export function useRefresh(): Refresh {
  return useContext(RefreshContext);
}
