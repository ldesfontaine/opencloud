import { useEffect, useRef, useState, type KeyboardEvent, type MouseEvent } from "react";

import type { Service } from "../api/types";
import { useT } from "../i18n/context";
import { fitView, internetID, nodeHeight, nodeWidth, zoomView, type Layout, type PlacedEdge, type PlacedNode, type View } from "../lib/network";
import { nodeSubtitle, nodeTone, serviceIcon } from "../lib/services";
import { useSize } from "../hooks/useSize";
import { Button } from "./Button";
import { iconPaths, type IconName } from "./Icon";
import "./NetworkGraph.scss";

// Le graphe de la planche « Machine · Réseau », en SVG : fond pointé,
// nœuds de 184 px, arêtes orthogonales arrondies, groupes en pointillé,
// nœud Internet en noir, sélection par anneau d'accent. Tout est attribut
// SVG, jamais un style en ligne, pour rester dans la CSP.

const zoomStep = 1.2;
// En dessous de ce déplacement, un glisser est un clic.
const clickSlack = 4;
// Ce qui tient entre l'icône et le point : Geist 13 et Geist Mono 11 sur
// 110 px. Internet n'a pas de point et gagne quelques signes.
const nameLimit = 16;
const subtitleLimit = 17;
const internetSubtitleLimit = 20;
// Une étiquette de port en mono 11 : la largeur d'un signe, et la marge.
const labelCharWidth = 6.6;
const labelPadding = 4;

interface Props {
  layout: Layout;
  ports: number[];
  selected: string | null;
  onSelect: (id: string | null) => void;
}

export function NetworkGraph({ layout, ports, selected, onSelect }: Props) {
  const t = useT();
  const holder = useRef<HTMLDivElement>(null);
  const { width, height } = useSize(holder);
  const [view, setView] = useState<View>({ scale: 1, x: 0, y: 0 });
  const drag = useRef<{ startX: number; startY: number; viewX: number; viewY: number; moved: boolean } | null>(null);

  // Recadrer quand la taille du graphe ou de la vue change ; le zoom de
  // l'opérateur survit à une relecture qui ne bouge rien.
  useEffect(() => {
    setView(fitView(layout, width, height));
  }, [layout.width, layout.height, width, height]);

  const onMouseDown = (event: MouseEvent<SVGSVGElement>) => {
    drag.current = { startX: event.clientX, startY: event.clientY, viewX: view.x, viewY: view.y, moved: false };
  };
  const onMouseMove = (event: MouseEvent<SVGSVGElement>) => {
    const current = drag.current;
    if (!current) {
      return;
    }
    const dx = event.clientX - current.startX;
    const dy = event.clientY - current.startY;
    if (Math.abs(dx) > clickSlack || Math.abs(dy) > clickSlack) {
      current.moved = true;
      setView((previous) => ({ ...previous, x: current.viewX + dx, y: current.viewY + dy }));
    }
  };
  const onMouseUp = () => {
    const current = drag.current;
    drag.current = null;
    if (current && !current.moved) {
      onSelect(null);
    }
  };
  const selectNode = (event: MouseEvent | KeyboardEvent, id: string) => {
    event.stopPropagation();
    onSelect(id);
  };

  const hot = new Set<string>();
  if (selected !== null) {
    for (const placed of layout.edges) {
      if (placed.edge.from === selected || placed.edge.to === selected) {
        hot.add(edgeKey(placed));
      }
    }
  }

  return (
    <div className="netgraph" ref={holder}>
      {width > 0 && (
        <svg
          width={width}
          height={height}
          className={drag.current?.moved ? "dragging" : undefined}
          onMouseDown={onMouseDown}
          onMouseMove={onMouseMove}
          onMouseUp={onMouseUp}
          onMouseLeave={onMouseUp}
          role="img"
          aria-label={t("tab.network")}
        >
          <defs>
            <pattern id="netgraph-dots" width="18" height="18" patternUnits="userSpaceOnUse">
              <circle className="netgraph-dot" cx="9" cy="9" r="1" />
            </pattern>
            <marker id="netgraph-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse">
              <path className="netgraph-arrow" d="M1 1 9 5 1 9" />
            </marker>
          </defs>
          <rect className="netgraph-bg" width={width} height={height} fill="url(#netgraph-dots)" />
          <g transform={`translate(${view.x} ${view.y}) scale(${view.scale})`}>
            {layout.groups.map((group) => (
              <g key={group.network_id} className="netgraph-group">
                <rect x={group.x} y={group.y} width={group.width} height={group.height} rx="16" />
                <text x={group.x + 14} y={group.y + 15}>
                  {group.internal ? t("network.group_internal", group.name) : group.name}
                </text>
              </g>
            ))}
            {layout.edges.map((placed) => (
              <EdgePath key={edgeKey(placed)} placed={placed} hot={hot.has(edgeKey(placed))} />
            ))}
            {layout.nodes.map((node) =>
              node.id === internetID ? (
                <InternetNode key={node.id} node={node} ports={ports} selected={selected === node.id} onSelect={selectNode} />
              ) : (
                <ServiceNode key={node.id} node={node} selected={selected === node.id} onSelect={selectNode} />
              ),
            )}
          </g>
        </svg>
      )}
      <div className="netgraph-legend">
        <span>
          <i className="solid" />
          {t("network.legend_public")}
        </span>
        <span>
          <i className="hot" />
          {t("network.legend_selected")}
        </span>
        <span>
          <i className="dashed" />
          {t("network.legend_depends")}
        </span>
      </div>
      <div className="netgraph-tools">
        <Button small icon="plus" aria-label={t("network.zoom_in")} title={t("network.zoom_in")} onClick={() => setView(zoomView(view, zoomStep, width, height))} />
        <Button small icon="minus" aria-label={t("network.zoom_out")} title={t("network.zoom_out")} onClick={() => setView(zoomView(view, 1 / zoomStep, width, height))} />
        <Button small onClick={() => setView(fitView(layout, width, height))}>
          {t("network.recenter")}
        </Button>
      </div>
    </div>
  );
}

function edgeKey(placed: PlacedEdge): string {
  return `${placed.edge.kind}:${placed.edge.from}>${placed.edge.to}:${placed.edge.port ?? ""}`;
}

function EdgePath({ placed, hot }: { placed: PlacedEdge; hot: boolean }) {
  const classes = ["netgraph-edge", placed.edge.kind];
  if (hot) {
    classes.push("hot");
  }
  return (
    <g className={classes.join(" ")}>
      <path d={placed.path} markerEnd={placed.edge.kind === "depends" ? "url(#netgraph-arrow)" : undefined} />
      {placed.edge.kind === "public" && placed.edge.port !== undefined && <PortLabel x={placed.labelX} y={placed.labelY} text={`:${placed.edge.port}`} />}
    </g>
  );
}

// Le port sur un fond de surface : lisible par-dessus un trait ou un
// pointillé de groupe.
function PortLabel({ x, y, text }: { x: number; y: number; text: string }) {
  const width = text.length * labelCharWidth + 2 * labelPadding;
  return (
    <>
      <rect className="label-bg" x={x - width / 2} y={y - 11} width={width} height={15} rx="4" />
      <text x={x} y={y} textAnchor="middle">
        {text}
      </text>
    </>
  );
}

interface NodeProps<T> {
  node: PlacedNode;
  selected: boolean;
  onSelect: (event: T, id: string) => void;
}

function InternetNode({ node, ports, selected, onSelect }: NodeProps<MouseEvent | KeyboardEvent> & { ports: number[] }) {
  const t = useT();
  const subtitle = ports.length > 0 ? t("network.internet_entries", fitPorts(ports, internetSubtitleLimit)) : t("network.internet_none");
  return (
    <NodeFrame
      node={node}
      selected={selected}
      onSelect={onSelect}
      className="netgraph-node internet"
      icon="globe"
      title={t("network.internet")}
      subtitle={subtitle}
      subtitleLimit={internetSubtitleLimit}
      tone={null}
    />
  );
}

// Autant de ports que la place permet, puis « +n » pour le reste.
export function fitPorts(ports: number[], limit: number): string {
  for (let count = ports.length; count > 0; count--) {
    const shown = ports.slice(0, count).map((port) => `:${port}`);
    if (count < ports.length) {
      shown.push(`+${ports.length - count}`);
    }
    const text = shown.join(" · ");
    if (text.length + "entrées ".length <= limit || count === 1) {
      return text;
    }
  }
  return "";
}

function ServiceNode({ node, selected, onSelect }: NodeProps<MouseEvent | KeyboardEvent>) {
  const service = node.service as Service;
  return (
    <NodeFrame
      node={node}
      selected={selected}
      onSelect={onSelect}
      className="netgraph-node"
      icon={serviceIcon(service)}
      title={service.name}
      subtitle={nodeSubtitle(service)}
      tone={nodeTone(service)}
    />
  );
}

function NodeFrame({
  node,
  selected,
  onSelect,
  className,
  icon,
  title,
  subtitle,
  subtitleLimit: limit = subtitleLimit,
  tone,
}: NodeProps<MouseEvent | KeyboardEvent> & { className: string; icon: IconName; title: string; subtitle: string; subtitleLimit?: number; tone: string | null }) {
  const onKeyDown = (event: KeyboardEvent<SVGGElement>) => {
    if (event.key === "Enter" || event.key === " ") {
      event.preventDefault();
      onSelect(event, node.id);
    }
  };
  return (
    <g
      className={selected ? `${className} selected` : className}
      transform={`translate(${node.x} ${node.y})`}
      role="button"
      tabIndex={0}
      aria-pressed={selected}
      onMouseDown={(event) => event.stopPropagation()}
      onClick={(event) => onSelect(event, node.id)}
      onKeyDown={onKeyDown}
    >
      <title>{`${title} · ${subtitle}`}</title>
      {selected && <rect className="ring" x="-3" y="-3" width={nodeWidth + 6} height={nodeHeight + 6} rx="15" />}
      <rect className="frame" width={nodeWidth} height={nodeHeight} rx="12" />
      <rect className="ico" x="11" y="11" width="32" height="32" rx="9" />
      <svg className="icon" x="18" y="18" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
        {iconPaths(icon)}
      </svg>
      <text className="name" x="52" y="24">
        {truncate(title, nameLimit)}
      </text>
      <text className="sub" x="52" y="40">
        {truncate(subtitle, limit)}
      </text>
      {tone !== null && <circle className={`dot ${tone}`} cx={nodeWidth - 16} cy={nodeHeight / 2} r="4" />}
    </g>
  );
}

// Un SVG ne coupe pas le texte tout seul : on coupe au nombre de signes.
function truncate(text: string, limit: number): string {
  return text.length > limit ? `${text.slice(0, limit - 1)}…` : text;
}
