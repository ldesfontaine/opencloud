import { useCallback, useState } from "react";

import { readViewMode, writeViewMode, type ViewMode } from "../lib/viewMode";

const browserStorage = () => window.localStorage;

// Le mode d'affichage d'une liste (cartes ou tableau), relu au montage et
// gardé à chaque bascule. `list` nomme la liste : chacune garde le sien.
export function useViewMode(list: string, fallback: ViewMode): [ViewMode, (mode: ViewMode) => void] {
  const [mode, setMode] = useState<ViewMode>(() => readViewMode(browserStorage, list, fallback));
  const choose = useCallback(
    (next: ViewMode) => {
      setMode(next);
      writeViewMode(browserStorage, list, next);
    },
    [list],
  );
  return [mode, choose];
}
