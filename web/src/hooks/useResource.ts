import { useCallback, useEffect, useState } from "react";

import { api, ApiError } from "../api/client";
import { useRefresh } from "../lib/refresh";

export interface Resource<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
  reload: () => void;
}

interface State<T> {
  data: T | null;
  error: ApiError | null;
  loading: boolean;
}

// Le chemin d'une ressource : tel quel, ou construit à la lecture quand il
// porte un paramètre de route passé par segment(), qui lève sur ce qui n'est
// pas un identifiant. Lever pendant le rendu ferait tomber toute la page ;
// ici l'erreur devient celle de la ressource, un 404 que la page sait montrer.
export type ResourcePath = string | (() => string) | null;

export function resolvePath(source: ResourcePath): { path: string | null; failure: ApiError | null } {
  if (typeof source !== "function") {
    return { path: source, failure: null };
  }
  try {
    return { path: source(), failure: null };
  } catch (error) {
    return { path: null, failure: error instanceof ApiError ? error : new ApiError(0, "internal") };
  }
}

// Lit une ressource de l'API et la relit quand le chemin change, quand la
// page le demande, ou quand le signal de rafraîchissement passe. Les
// données précédentes restent à l'écran pendant la relecture.
export function useResource<T>(source: ResourcePath): Resource<T> {
  const { path, failure } = resolvePath(source);
  const version = useRefresh().versionFor(path ?? "");
  const [tick, setTick] = useState(0);
  const [state, setState] = useState<State<T>>({ data: null, error: null, loading: path !== null });

  useEffect(() => {
    if (path === null) {
      return;
    }
    let cancelled = false;
    setState((current) => ({ ...current, loading: true }));
    api
      .get<T>(path)
      .then((data) => {
        if (!cancelled) {
          setState({ data, error: null, loading: false });
        }
      })
      .catch((error: unknown) => {
        if (!cancelled) {
          const failed = error instanceof ApiError ? error : new ApiError(0, "internal");
          setState((current) => ({ data: current.data, error: failed, loading: false }));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [path, version, tick]);

  const reload = useCallback(() => setTick((current) => current + 1), []);
  if (failure !== null) {
    return { data: null, error: failure, loading: false, reload };
  }
  return { ...state, reload };
}
