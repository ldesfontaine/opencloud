import { useCallback, useEffect, useState } from "react";

import { copyText } from "../lib/clipboard";

const shownFor = 1500;

export type CopyState = "idle" | "copied" | "failed";

// Copie un texte et dit « Copié », ou que la copie n'a pas pu se faire, un
// instant.
export function useCopy(): { state: CopyState; copy: (text: string) => void } {
  const [state, setState] = useState<CopyState>("idle");
  useEffect(() => {
    if (state === "idle") {
      return;
    }
    const timer = window.setTimeout(() => setState("idle"), shownFor);
    return () => window.clearTimeout(timer);
  }, [state]);
  const copy = useCallback((text: string) => {
    void copyText(text).then((done) => setState(done ? "copied" : "failed"));
  }, []);
  return { state, copy };
}
