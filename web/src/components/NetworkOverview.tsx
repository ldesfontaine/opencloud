import { useMemo, useRef } from "react";
import { useNavigate } from "react-router";

import type { OverviewNetworkResponse } from "../api/types";
import { useSize } from "../hooks/useSize";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { compactHeight, compactWidth, layoutOverview, nodeHeight, nodeWidth, publicPorts } from "../lib/network";
import { nodeTone, serviceIcon } from "../lib/services";
import { Card, Empty } from "./Card";
import { Failure } from "./Failure";
import { iconPaths } from "./Icon";
import { fitPorts } from "./NetworkGraph";
import "./NetworkGraph.scss";
import "./NetworkOverview.scss";

const nameLimit = 14;
const internetSubtitleLimit = 20;

// Le réseau de la vue d'ensemble : toutes les machines dans un seul dessin,
// sans zoom, mis à l'échelle de la carte. Un nœud mène à la fiche du
// service, le nom d'une machine à son onglet Réseau.
export function NetworkOverview() {
  const t = useT();
  const navigate = useNavigate();
  const holder = useRef<HTMLDivElement>(null);
  const width = useSize(holder).width;
  const network = useResource<OverviewNetworkResponse>("/api/network");
  const response = network.data;
  const layout = useMemo(() => (response ? layoutOverview(response) : null), [response]);
  if (network.error && !response) {
    return <Failure error={network.error} />;
  }
  if (response && response.machines.every((machine) => machine.services.length === 0)) {
    return (
      <Card>
        <Empty icon="network" text={t("network.overview_empty")} />
      </Card>
    );
  }
  const ports = publicPorts(response?.machines.flatMap((machine) => machine.edges) ?? []);
  const entries = ports.length > 0 ? t("network.internet_entries", fitPorts(ports, internetSubtitleLimit)) : t("network.internet_none");
  const scale = layout && width > 0 ? Math.min(1, width / layout.width) : 1;
  // Le conteneur est toujours là : c'est lui qui donne la largeur, avant
  // même que la réponse arrive.
  return (
    <div className="netgraph overview" ref={holder}>
      {response && layout && width > 0 && (
        <svg width={width} height={Math.ceil(layout.height * scale)} role="img" aria-label={t("tab.network")}>
          <defs>
            <pattern id="netgraph-dots" width="18" height="18" patternUnits="userSpaceOnUse">
              <circle className="netgraph-dot" cx="9" cy="9" r="1" />
            </pattern>
          </defs>
          <rect className="netgraph-bg" width={width} height={Math.ceil(layout.height * scale)} fill="url(#netgraph-dots)" />
          <g transform={`scale(${scale})`}>
            {layout.framed &&
              layout.machines.map((machine) => (
                <g key={machine.id} className="netgraph-group machine" onClick={() => void navigate(`/machines/${machine.id}/reseau`)} role="link" tabIndex={0}>
                  <rect x={machine.x} y={machine.y} width={machine.width} height={machine.height} rx="16" />
                  <circle className={`dot ${machine.online ? "ok" : "danger"}`} cx={machine.x + 18} cy={machine.y + 12} r="3" />
                  <text x={machine.x + 26} y={machine.y + 15}>
                    {machine.name}
                  </text>
                  {machine.empty && (
                    <text className="empty" x={machine.x + 16} y={machine.y + 44}>
                      {t("network.machine_empty")}
                    </text>
                  )}
                </g>
              ))}
            {layout.edges.map((placed) => (
              <g key={`${placed.edge.to}:${placed.edge.port ?? ""}`} className="netgraph-edge public">
                <path d={placed.path} />
              </g>
            ))}
            <g className="netgraph-node internet" transform={`translate(${layout.internet.x} ${layout.internet.y})`}>
              <rect className="frame" width={nodeWidth} height={nodeHeight} rx="12" />
              <rect className="ico" x="11" y="11" width="32" height="32" rx="9" />
              <svg className="icon" x="18" y="18" width="18" height="18" viewBox="0 0 24 24" aria-hidden="true">
                {iconPaths("globe")}
              </svg>
              <text className="name" x="52" y="24">
                {t("network.internet")}
              </text>
              <text className="sub" x="52" y="40">
                {entries.length > internetSubtitleLimit ? `${entries.slice(0, internetSubtitleLimit - 1)}…` : entries}
              </text>
            </g>
            {layout.nodes.map((node) => {
              const service = node.service;
              if (service === null) {
                return null;
              }
              return (
                <g
                  key={node.id}
                  className="netgraph-node compact"
                  transform={`translate(${node.x} ${node.y})`}
                  role="link"
                  tabIndex={0}
                  onClick={() => void navigate(`/services/${service.id}`)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") {
                      void navigate(`/services/${service.id}`);
                    }
                  }}
                >
                  <title>{`${service.name} · ${service.image}`}</title>
                  <rect className="frame" width={compactWidth} height={compactHeight} rx="10" />
                  <rect className="ico" x="8" y="8" width="24" height="24" rx="7" />
                  <svg className="icon" x="13" y="13" width="14" height="14" viewBox="0 0 24 24" aria-hidden="true">
                    {iconPaths(serviceIcon(service))}
                  </svg>
                  <text className="name" x="40" y="25">
                    {service.name.length > nameLimit ? `${service.name.slice(0, nameLimit - 1)}…` : service.name}
                  </text>
                  <circle className={`dot ${nodeTone(service)}`} cx={compactWidth - 14} cy={compactHeight / 2} r="3.5" />
                </g>
              );
            })}
          </g>
        </svg>
      )}
    </div>
  );
}
