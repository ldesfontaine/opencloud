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

// Lit une ressource de l'API et la relit quand le chemin change, quand la
// page le demande, ou quand le signal de rafraîchissement passe. Les
// données précédentes restent à l'écran pendant la relecture.
export function useResource<T>(path: string | null): Resource<T> {
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
          const failure = error instanceof ApiError ? error : new ApiError(0, "internal");
          setState((current) => ({ data: current.data, error: failure, loading: false }));
        }
      });
    return () => {
      cancelled = true;
    };
  }, [path, version, tick]);

  const reload = useCallback(() => setTick((current) => current + 1), []);
  return { ...state, reload };
}
