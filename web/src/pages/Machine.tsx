import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { api } from "../api/client";
import type { Current, Machine, MachinesResponse, ProbesResponse, ContainersResponse, TokenResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { CertificateList } from "../components/Certificate";
import { Failure } from "../components/Failure";
import { MachineHead, Tabs } from "../components/MachineHead";
import { NetworkView } from "../components/NetworkView";
import { PageHead } from "../components/PageHead";
import { ProbeTable } from "../components/ProbeTable";
import { ResourceHistory, ResourceStats } from "../components/Resources";
import { ContainerFilter, ContainerTable } from "../components/ContainerTable";
import { SupportBlock } from "../components/SupportBlock";
import { ContainerPill } from "../components/Pill";
import { Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { resolveMachineTab } from "../lib/machineTabs";
import { useRefresh } from "../api/refresh";
import { engineNote, filterContainers, type Filter } from "../lib/containers";
import { seenAgo } from "../lib/time";
import { NotFound } from "./NotFound";

// La page d'une machine : l'en-tête de la direction artistique, les
// onglets, le résumé avec ses quatre chiffres clés et l'historique, les
// services, les domaines, le réseau, le raccourci des journaux. Un onglet pas encore livré renvoie au
// résumé.
export function MachinePage() {
  const { id = "", tab = "" } = useParams();
  const resolved = resolveMachineTab(tab);
  if (resolved === "summary") {
    return <Navigate to={`/machines/${id}`} replace />;
  }
  return <KnownMachinePage id={id} tab={tab} known={resolved === "shown"} />;
}

function KnownMachinePage({ id, tab, known }: { id: string; tab: string; known: boolean }) {
  const machines = useResource<MachinesResponse>("/api/machines");
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (!known || (machines.data && !current)) {
    return <NotFound />;
  }
  if (!machines.data || !current) {
    return machines.error ? <Failure error={machines.error} /> : null;
  }
  return <MachineView current={current} all={machines.data.machines} tab={tab} />;
}

function MachineView({ current, all, tab }: { current: Machine; all: Machine[]; tab: string }) {
  const t = useT();
  const now = useNow();
  const resources = useResource<Current>(tab === "" ? `/api/machines/${current.id}/resources` : null);
  return (
    <>
      <PageHead title={current.name} actions={!current.local && <Actions machine={current} />} />
      <Card>
        <CardBody gap={16}>
          <MachineHead current={current} all={all} />
          <Tabs machineID={current.id} />
        </CardBody>
      </Card>
      {tab === "" ? (
        <>
          <ResourceStats current={resources.data} />
          <ResourceHistory machineID={current.id} />
          <Card>
            <CardHeader title={t("tab.summary")} />
            <CardBody gap={10}>
              <KeyValue label={t("machine.field_kind")} value={t(current.local ? "machine.kind_local" : "machine.kind_remote")} />
              <KeyValue label={t("machine.field_address")} value={current.address} mono />
              <KeyValue label={t("machine.field_os")} value={current.os} />
              <KeyValue label={t("machine.field_agent")} value={current.agent_version} mono />
              <KeyValue label={t("machine.field_seen")} value={seenAgo(t, current.last_seen_at, now)} />
              {current.lifecycle !== undefined && <SupportBlock lifecycle={current.lifecycle} subject="system" />}
            </CardBody>
          </Card>
        </>
      ) : tab === "services" ? (
        <MachineContainers machineID={current.id} />
      ) : tab === "reseau" ? (
        <NetworkView machineID={current.id} />
      ) : tab === "journaux" ? (
        <MachineLogs machineID={current.id} />
      ) : (
        <MachineProbes machineID={current.id} />
      )}
    </>
  );
}

// L'onglet Domaines et certificats : les sondes que cette machine exécute,
// puis les certificats qu'elles ont vus, avec ce qu'il leur reste.
function MachineProbes({ machineID }: { machineID: string }) {
  const t = useT();
  const probes = useResource<ProbesResponse>(`/api/machines/${machineID}/probes`);
  const list = probes.data?.probes ?? [];
  return (
    <>
      {probes.error && <Failure error={probes.error} />}
      {probes.data && list.length === 0 && (
        <Card>
          <Empty icon="globe" text={t("probes.machine_empty")} />
        </Card>
      )}
      {probes.data && list.length > 0 && (
        <>
          <Card className="scroll-x">
            <ProbeTable probes={list} days={probes.data.days} />
          </Card>
          <CertificateList probes={list} />
        </>
      )}
    </>
  );
}

// L'onglet Services : le tableau de la planche avec son filtre ; sans
// conteneur, ce que la machine a dit de son Docker.
function MachineContainers({ machineID }: { machineID: string }) {
  const t = useT();
  const containers = useResource<ContainersResponse>(`/api/machines/${machineID}/containers`);
  const [filter, setFilter] = useState<Filter>("all");
  const all = containers.data?.containers ?? [];
  const shown = filterContainers(all, filter);
  const note = containers.data && all.length === 0 ? engineNote(t, containers.data.engines[0]) : null;
  return (
    <Card className="scroll-x">
      <CardHeader title={t("tab.services")} aside={containers.data ? t("services.machine_subtitle", all.length) : ""} />
      {containers.error && <Failure error={containers.error} />}
      {all.length > 0 && (
        <div className="table-bar">
          <ContainerFilter value={filter} onChange={setFilter} />
        </div>
      )}
      {note !== null && (
        <CardBody>
          <Note tone="warn">{note}</Note>
        </CardBody>
      )}
      {containers.data && all.length === 0 && note === null && <Empty text={t("services.empty_title")} />}
      {shown.length > 0 && <ContainerTable containers={shown} withMachine={false} />}
      {all.length > 0 && shown.length === 0 && <Empty text={t("services.empty_title")} />}
    </Card>
  );
}

// L'onglet Journaux : un raccourci. Les journaux se lisent sur la fiche
// du conteneur ; chaque ligne y mène, à l'ancre des journaux.
function MachineLogs({ machineID }: { machineID: string }) {
  const t = useT();
  const containers = useResource<ContainersResponse>(`/api/machines/${machineID}/containers`);
  const all = containers.data?.containers ?? [];
  const note = containers.data && all.length === 0 ? engineNote(t, containers.data.engines[0]) : null;
  return (
    <Card>
      <CardHeader title={t("tab.logs")} aside={containers.data ? t("services.machine_subtitle", all.length) : ""} />
      {containers.error && <Failure error={containers.error} />}
      {note !== null && (
        <CardBody>
          <Note tone="warn">{note}</Note>
        </CardBody>
      )}
      {containers.data && all.length === 0 && note === null && <Empty icon="file" text={t("logs.machine_empty")} />}
      {all.length > 0 && (
        <Table>
          <thead>
            <tr>
              <th>{t("services.col_container")}</th>
              <th className="wide-only">{t("services.col_state")}</th>
              <th className="th-end">{t("logs.col_action")}</th>
            </tr>
          </thead>
          <tbody>
            {all.map((container) => (
              <tr key={container.id}>
                <td>
                  <Subject icon="box">
                    <span className="svc-name">
                      <span>{container.name}</span>
                      <span className="narrow-only">
                        <ContainerPill container={container} />
                      </span>
                    </span>
                  </Subject>
                </td>
                <td className="wide-only">
                  <ContainerPill container={container} />
                </td>
                <td className="td-end">
                  <Button small icon="file" to={`/services/conteneurs/${container.id}#journaux`}>
                    {t("logs.open")}
                  </Button>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

// Ré-enrôler donne un nouveau jeton pour le même id ; retirer coupe l'agent
// et efface l'historique, après confirmation.
function Actions({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);

  const reenroll = () => {
    api
      .post<TokenResponse>(`/api/machines/${machine.id}/actions/reenroll`)
      .then((token) => {
        refresh();
        void navigate("/machines/jeton", { state: token });
      })
      .catch(setError);
  };
  const remove = () => {
    if (!window.confirm(t("machine.remove_confirm", machine.name))) {
      return;
    }
    api
      .delete(`/api/machines/${machine.id}`)
      .then(() => {
        refresh();
        void navigate("/machines");
      })
      .catch(setError);
  };

  return (
    <>
      {error !== null && <Failure error={error} />}
      <Button icon="refresh" onClick={reenroll}>
        {t("machine.reenroll")}
      </Button>
      <Button variant="danger" icon="x" onClick={remove}>
        {t("machine.remove")}
      </Button>
    </>
  );
}
