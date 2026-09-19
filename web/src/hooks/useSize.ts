import { useEffect, useState, type RefObject } from "react";

export interface Size {
  width: number;
  height: number;
}

// La taille d'un conteneur, suivie : un SVG se dessine en pixels. La
// première mesure est prise tout de suite : un onglet qui ne peint pas
// encore ne livre pas d'observation, et le dessin l'attendrait.
export function useSize(holder: RefObject<HTMLDivElement | null>): Size {
  const [size, setSize] = useState<Size>({ width: 0, height: 0 });
  useEffect(() => {
    const element = holder.current;
    if (!element) {
      return;
    }
    const box = element.getBoundingClientRect();
    setSize({ width: Math.floor(box.width), height: Math.floor(box.height) });
    const observer = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setSize({ width: Math.floor(entry.contentRect.width), height: Math.floor(entry.contentRect.height) });
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [holder]);
  return size;
}
