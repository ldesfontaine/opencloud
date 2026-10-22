import { useLayoutEffect, useRef, useState } from "react";
import { NavLink, useLocation } from "react-router";

import { useSize } from "../hooks/useSize";
import { useT } from "../i18n/context";
import { edgesOf, type TabEdges } from "../lib/tabs";

export interface Tab {
  to: string;
  // La clé du catalogue de l'intitulé.
  key: string;
}

// Une barre d'onglets : un lien par onglet, actif sur son adresse exacte.
// Quand elle déborde (un téléphone), un fondu au bord signale ce qui est
// caché, et l'onglet actif est amené dans la vue.
export function TabNav({ tabs }: { tabs: readonly Tab[] }) {
  const t = useT();
  const { pathname } = useLocation();
  const holder = useRef<HTMLElement>(null);
  const size = useSize(holder);
  const [edges, setEdges] = useState<TabEdges>({ left: false, right: false });

  // À chaque rendu : les intitulés changent de largeur avec la langue ou un
  // compteur, sans que la barre elle-même change de taille.
  const measure = () => {
    const bar = holder.current;
    if (!bar) {
      return;
    }
    const next = edgesOf(bar.scrollLeft, bar.scrollWidth, bar.clientWidth);
    setEdges((previous) => (previous.left === next.left && previous.right === next.right ? previous : next));
  };
  useLayoutEffect(measure, [size.width, tabs, pathname]);

  useLayoutEffect(() => {
    holder.current?.querySelector(".active")?.scrollIntoView({ inline: "nearest", block: "nearest" });
  }, [pathname]);

  const fade = edges.left && edges.right ? " fade-both" : edges.left ? " fade-left" : edges.right ? " fade-right" : "";
  return (
    <nav className={`tabs${fade}`} ref={holder} onScroll={measure}>
      {tabs.map((tab) => (
        <NavLink key={tab.to} className={({ isActive }) => (isActive ? "tab active" : "tab")} to={tab.to} end>
          {t(tab.key)}
        </NavLink>
      ))}
    </nav>
  );
}
