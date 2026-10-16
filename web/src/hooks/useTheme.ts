import { useEffect, useState } from "react";

export type Theme = "light" | "dark";

function currentTheme(): Theme {
  return document.documentElement.dataset.theme === "dark" ? "dark" : "light";
}

// Le thème appliqué par theme.js, lu sur <html data-theme> et suivi quand
// il change : le script reste seul à le choisir, React ne fait que l'annoncer.
export function useTheme(): Theme {
  const [theme, setTheme] = useState<Theme>(() => currentTheme());
  useEffect(() => {
    const observer = new MutationObserver(() => setTheme(currentTheme()));
    observer.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    return () => observer.disconnect();
  }, []);
  return theme;
}
