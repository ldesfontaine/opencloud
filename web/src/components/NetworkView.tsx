import { useMemo, useState } from "react";

import type { NetworkResponse, ProbesResponse } from "../api/types";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { layoutNetwork, publicPorts } from "../lib/network";
import { engineNote } from "../lib/services";
import { Card, CardBody, Empty, Note } from "./Card";
import { Failure } from "./Failure";
import { NetworkGraph } from "./NetworkGraph";
import { NetworkInspector } from "./NetworkInspector";
import { ServiceFilter, useServiceFilter } from "./ServiceTable";
import "./NetworkView.scss";

// L'onglet Réseau d'une machine : le filtre de l'onglet Services, le
// graphe et l'inspecteur. Le direct relit la topologie sur le sujet
// services ; la sélection tient par identifiant, elle survit à la relecture.
export function NetworkView({ machineID }: { machineID: string }) {
  const t = useT();
  const network = useResource<NetworkResponse>(`/api/machines/${machineID}/network`);
  // Les sondes de cette machine servent la ligne « Certificat » de
  // l'inspecteur : le graphe dit les liens, la sonde dit ce qu'elle a vu.
  const probes = useResource<ProbesResponse>(`/api/machines/${machineID}/probes`);
  const [filter, setFilter] = useServiceFilter();
  const [selected, setSelected] = useState<string | null>(null);
  const response = network.data;
  const layout = useMemo(() => (response ? layoutNetwork(response, filter) : null), [response, filter]);
  if (network.error && !response) {
    return <Failure error={network.error} />;
  }
  if (!response || !layout) {
    return null;
  }
  if (response.services.length === 0) {
    const note = engineNote(t, response.engine ?? undefined);
    return (
      <Card>
        {note !== null && (
          <CardBody>
            <Note tone="warn">{note}</Note>
          </CardBody>
        )}
        {note === null && <Empty icon="network" text={t("network.empty")} />}
      </Card>
    );
  }
  const visible = layout.nodes.length - 1;
  return (
    <div className="network-view">
      {network.error && <Failure error={network.error} />}
      <div className="table-bar network-bar">
        <ServiceFilter value={filter} onChange={setFilter} />
        <span className="secondary">{t("network.shown", visible, response.services.length)}</span>
      </div>
      <div className="network-body">
        <NetworkGraph layout={layout} ports={publicPorts(response.edges)} selected={selected} onSelect={setSelected} />
        <NetworkInspector response={response} probes={probes.data?.probes ?? []} selected={selected} onSelect={setSelected} />
      </div>
    </div>
  );
}
