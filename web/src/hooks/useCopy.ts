import { useCallback, useEffect, useState } from "react";

const copiedFor = 1500;

// Copie un texte et dit « Copié » un instant.
export function useCopy(): { copied: boolean; copy: (text: string) => void } {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) {
      return;
    }
    const timer = window.setTimeout(() => setCopied(false), copiedFor);
    return () => window.clearTimeout(timer);
  }, [copied]);
  const copy = useCallback((text: string) => {
    navigator.clipboard
      .writeText(text)
      .then(() => setCopied(true))
      .catch(() => setCopied(false));
  }, []);
  return { copied, copy };
}
