import { useCallback, useLayoutEffect, useState } from "react";

import type { NetworkMap } from "../../api/network";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { type Edge, type Rect, anchorPath, drawnEdges, linkTip, serviceIndex } from "../../lib/netmap";
import { iconPaths } from "../Icon";

interface Measured {
  width: number;
  height: number;
  rects: Record<string, Rect>;
}

const none: Measured = { width: 0, height: 0, rects: {} };

// Les ancres du dessin (data-anchor) mesurées dans le repère du conteneur : le
// calque SVG se pose par-dessus des boîtes HTML qui, elles, se placent seules.
// Une mesure par changement de la carte, des plis ou de la taille.
function useAnchors(root: HTMLElement | null, dependencies: readonly unknown[]): Measured {
  const [measured, setMeasured] = useState<Measured>(none);
  const measure = useCallback(() => {
    if (root === null) {
      return;
    }
    const origin = root.getBoundingClientRect();
    const rects: Record<string, Rect> = {};
    for (const anchor of root.querySelectorAll<HTMLElement>("[data-anchor]")) {
      const box = anchor.getBoundingClientRect();
      if (box.width === 0 && box.height === 0) {
        continue;
      }
      rects[anchor.dataset.anchor ?? ""] = { left: box.left - origin.left, top: box.top - origin.top, right: box.right - origin.left, bottom: box.bottom - origin.top };
    }
    const next = { width: Math.ceil(origin.width), height: Math.ceil(origin.height), rects };
    setMeasured((known) => (JSON.stringify(known) === JSON.stringify(next) ? known : next));
  }, [root]);
  useLayoutEffect(() => {
    if (root === null) {
      return;
    }
    measure();
    const observer = new ResizeObserver(measure);
    observer.observe(root);
    return () => observer.disconnect();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [measure, ...dependencies]);
  return measured;
}

interface Tip {
  key: string;
  x: number;
  y: number;
}

// Le calque des traits : entrées d'Internet, liens, tunnels. Chaque
// trait qui porte une preuve la dit au survol et au clavier (focus) ; la même
// information se lit dans la liste des liens sous la carte.
// watch : ce qui déplace des cartes sans changer la taille du conteneur.
export function LinkLayer({ map, folded, root, watch }: { map: NetworkMap; folded: ReadonlySet<string>; root: HTMLElement | null; watch: readonly unknown[] }) {
  const t = useT();
  const now = useNow(60_000);
  const measured = useAnchors(root, [map, folded, ...watch]);
  const [tip, setTip] = useState<Tip | null>(null);
  const edges = drawnEdges(map, folded);
  const spots = serviceIndex(map);
  const placed = edges.flatMap((edge) => {
    const from = measured.rects[edge.from];
    const to = measured.rects[edge.to];
    return from === undefined || to === undefined ? [] : [{ edge, ...anchorPath(from, to, edge.kind === "tunnel") }];
  });
  const shown = placed.find((item) => item.edge.key === tip?.key);
  return (
    <>
      <svg className="netmap-links" width={measured.width} height={measured.height} aria-hidden={tip === null ? true : undefined}>
        <defs>
          <marker id="netmap-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="7" markerHeight="7" orient="auto">
            <path className="netmap-arrow" d="M1 1 9 5 1 9" />
          </marker>
        </defs>
        {placed.map(({ edge, path, middle }) => (
          <g
            key={edge.key}
            className={edgeClass(edge)}
            tabIndex={0}
            role="img"
            aria-label={tipText(t, edge, spots, now)}
            onPointerMove={(event) => setTip({ key: edge.key, ...pointOf(event, root) })}
            onPointerLeave={() => setTip(null)}
            onFocus={() => setTip({ key: edge.key, x: middle.x, y: middle.y })}
            onBlur={() => setTip(null)}
          >
            <path className="netmap-hit" d={path} />
            <path className="netmap-line" d={path} markerEnd={edge.kind === "link" ? "url(#netmap-arrow)" : undefined} />
            {edge.kind === "tunnel" && (
              <>
                <svg className="netmap-lock icon" x={middle.x - 10} y={middle.y - 10} width="20" height="20" viewBox="0 0 24 24" aria-hidden="true">
                  <circle className="netmap-lock-back" cx="12" cy="12" r="12" />
                  {iconPaths("lock")}
                </svg>
                <text className="netmap-edge-label" x={middle.x + 16} y={middle.y + 4}>
                  {t("network.map_tunnel", edge.tunnel.label)}
                </text>
              </>
            )}
            {edge.kind === "link" && edge.link.proposed && (
              <g className="netmap-pending" transform={`translate(${middle.x} ${middle.y})`}>
                <circle r="8" />
                <text y="4" textAnchor="middle">
                  ?
                </text>
              </g>
            )}
          </g>
        ))}
      </svg>
      {shown !== undefined && tip !== null && (
        <div className="netmap-tip" role="tooltip" style={{ left: tip.x, top: tip.y }}>
          <TipBody t={t} edge={shown.edge} spots={spots} now={now} />
        </div>
      )}
    </>
  );
}

function edgeClass(edge: Edge): string {
  switch (edge.kind) {
    case "entry":
      return `netmap-edge entry${edge.entry.observed ? " is-observed" : ""}`;
    case "tunnel":
      return `netmap-edge tunnel proof-${edge.tunnel.proof}`;
    case "link":
      return `netmap-edge link proof-${edge.link.proof}${edge.link.stale ? " is-stale" : ""}${edge.link.proposed ? " is-proposed" : ""}`;
  }
}

function pointOf(event: React.PointerEvent, root: HTMLElement | null): { x: number; y: number } {
  const origin = root?.getBoundingClientRect();
  return { x: event.clientX - (origin?.left ?? 0), y: event.clientY - (origin?.top ?? 0) };
}

type Translate = ReturnType<typeof useT>;
type Spots = ReturnType<typeof serviceIndex>;

function tipLines(t: Translate, edge: Edge, spots: Spots, now: number): { title: string; lines: string[] } {
  switch (edge.kind) {
    case "link":
      return linkTip(t, edge.link, spots, now);
    case "entry": {
      const lines = [t("network.map_entry", edge.entry.ports.join(", "))];
      if (edge.entry.observed) {
        lines.push(t("network.map_entry_seen"));
      }
      return { title: `${t("network.internet")} → ${spots.get(edge.entry.to)?.service.label ?? ""}`, lines };
    }
    case "tunnel":
      return {
        title: t("network.map_tunnel", edge.tunnel.label),
        lines: [t(`link.proof_${edge.tunnel.proof}`), edge.tunnel.firewall === "" ? t("network.map_tunnel_alone") : t("network.map_tunnel_via", edge.tunnel.interface)],
      };
  }
}

function tipText(t: Translate, edge: Edge, spots: Spots, now: number): string {
  const { title, lines } = tipLines(t, edge, spots, now);
  return [title, ...lines].join(" · ");
}

function TipBody({ t, edge, spots, now }: { t: Translate; edge: Edge; spots: Spots; now: number }) {
  const { title, lines } = tipLines(t, edge, spots, now);
  return (
    <>
      <strong>{title}</strong>
      {lines.map((line) => (
        <span key={line}>{line}</span>
      ))}
    </>
  );
}
