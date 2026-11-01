import { useEffect, useState } from "react";
import { Navigate, Outlet, useLocation, useNavigate, useParams } from "react-router";

import { useMachineContainers } from "../api/containers";
import type { Lifecycle } from "../api/lifecycle";
import { deleteMachine, useMachines, type Agent, type Machine } from "../api/machines";
import { useMachineProbes } from "../api/probes";
import { useMachineResources } from "../api/resources";
import { ActionsCard } from "../components/ActionsCard";
import { AgentUpdate } from "../components/AgentUpdate";
import { AgentGate } from "../components/AgentGate";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, KeyValue, Note } from "../components/Card";
import { CertificateList } from "../components/Certificate";
import { DeleteMachineDialog } from "../components/DeleteMachineDialog";
import { Failure } from "../components/Failure";
import { MachineHead, Tabs } from "../components/MachineHead";
import { McpChip } from "../components/McpChip";
import { MachineHypervisor } from "../components/MachineHypervisor";
import { MachineIdentity } from "../components/MachineIdentity";
import { MachineStorage } from "../components/MachineStorage";
import { MachineTodoCard } from "../components/MachineTodoCard";
import { MachineSSHKeys } from "../components/MachineSSHKeys";
import { NetworkView } from "../components/NetworkView";
import { PageHead } from "../components/PageHead";
import { RoleCard } from "../components/RoleCard";
import { ProbeTable } from "../components/ProbeTable";
import { ResourceHistory, ResourceStats } from "../components/Resources";
import { ContainerFilter, ContainerTable } from "../components/ContainerTable";
import { SupportDetails } from "../components/SupportBlock";
import { ContainerPill } from "../components/Pill";
import { Subject, Table } from "../components/Table";
import { useNow } from "../hooks/useNow";
import { useT } from "../i18n/context";
import { renamedMachineTabs, resolveMachineTab } from "../lib/machineTabs";
import { engineNote, filterContainers, type Filter } from "../lib/containers";
import { lifecycleHasDetails } from "../lib/lifecycle";
import { describeAccount, describeCapability, isArchived, unavailableRights } from "../lib/machines";
import { formatWhen } from "../lib/time";
import { NotFound } from "./NotFound";
import "./Machine.scss";

// La page d'une machine : l'en-tête de la direction artistique, les
// onglets, le résumé avec ses quatre chiffres clés et l'historique, les
// services, la disponibilité, le réseau, le raccourci des journaux. Un onglet pas encore livré renvoie au
// résumé.
export function MachinePage() {
  const { id = "", tab = "" } = useParams();
  const location = useLocation();
  const resolved = resolveMachineTab(tab);
  if (resolved === "summary") {
    return <Navigate to={`/machines/${id}`} replace />;
  }
  if (resolved === "renamed") {
    return <Navigate to={{ pathname: `/machines/${id}/${renamedMachineTabs[tab]}`, search: location.search, hash: location.hash }} replace />;
  }
  return <KnownMachinePage id={id} tab={tab} known={resolved === "shown"} />;
}

function KnownMachinePage({ id, tab, known }: { id: string; tab: string; known: boolean }) {
  const machines = useMachines();
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (!known || (machines.data && !current)) {
    return <NotFound />;
  }
  if (!machines.data || !current) {
    return machines.error ? <Failure error={machines.error} /> : null;
  }
  return <MachineView current={current} all={machines.data.machines} tab={tab} />;
}

// 16 px entre les blocs de la page (.content, global, garde ses 24 px) : en
// tête, tuiles et historique tiennent sur un écran de portable (#141). Deux
// colonnes : l'identité fixe à droite (où tourne la machine), les onglets à
// gauche ; sous 1100 px l'identité passe devant.
function MachineView({ current, all, tab }: { current: Machine; all: Machine[]; tab: string }) {
  const now = useNow();
  const resources = useMachineResources(tab === "" ? current.id : null);
  // Un lien « détail du support » mène à son ancre sur la page même.
  const { hash } = useLocation();
  useEffect(() => {
    if (hash !== "") {
      document.getElementById(hash.slice(1))?.scrollIntoView({ block: "start" });
    }
  }, [hash]);
  return (
    <div className="machine-page">
      <PageHead title={current.name} actions={<Actions machine={current} />} />
      <Card>
        <CardBody gap={12}>
          <MachineHead current={current} all={all} />
          <Tabs machineID={current.id} modules={current.modules} />
        </CardBody>
      </Card>
      <div className="machine-columns">
        <div className="machine-main">
          {tab === "" ? (
            <>
              <MachineTodoCard machine={current} />
              <ResourceStats current={resources.data} />
              <ResourceHistory machineID={current.id} />
              <MachineStorage machine={current} current={resources.data} />
              {current.lifecycle !== undefined && lifecycleHasDetails(current.lifecycle) && <SystemSupport lifecycle={current.lifecycle} />}
              <RoleCard machine={current} />
            </>
          ) : tab === "agent" ? (
            <>
              <AgentCard machineID={current.id} agent={current.agent} updateLast={current.update_last} now={now} />
              <ActionsCard machineID={current.id} agent={current.agent} />
            </>
          ) : tab === "services" ? (
            <MachineContainers machineID={current.id} />
          ) : tab === "reseau" ? (
            <NetworkView machineID={current.id} />
          ) : tab === "ssh" ? (
            <MachineSSHKeys machine={current} />
          ) : tab === "hyperviseur" ? (
            <MachineHypervisor machine={current} />
          ) : tab === "journaux" ? (
            <MachineLogs machineID={current.id} />
          ) : (
            <MachineProbes machineID={current.id} />
          )}
        </div>
        <aside className="machine-aside">
          <MachineIdentity machine={current} all={all} />
        </aside>
      </div>
      {/* Les fenêtres de l'adresse : ré-enrôler, déclarer l'hôte Proxmox, corriger le type et l'hôte. */}
      <Outlet />
    </div>
  );
}

// Le détail du support du système : l'état et sa phrase sont dans l'en-tête,
// la carte garde le reste (branches, lecture, source).
function SystemSupport({ lifecycle }: { lifecycle: Lifecycle }) {
  const t = useT();
  return (
    <div id="support">
      <Card>
        <CardHeader title={t("lifecycle.title_system")} />
        <CardBody gap={8}>
          <SupportDetails lifecycle={lifecycle} />
        </CardBody>
      </Card>
    </div>
  );
}

// L'agent de la machine : sa version, depuis quand, s'il est révoqué, et ses
// droits, tels qu'il les rapporte et tels que sa commande d'installation les
// demandait. Il applique ceux de son fichier ; les changer, c'est le
// ré-enrôler, dans la fenêtre du même nom.
function AgentCard({ machineID, agent, updateLast, now }: { machineID: string; agent: Agent | null; updateLast: boolean; now: number }) {
  const t = useT();
  if (agent === null) {
    return (
      <Card>
        <CardHeader title={t("machine.agent_title")} />
        <CardBody>
          <Note tone="warn">{t("machine.agent_none")}</Note>
        </CardBody>
      </Card>
    );
  }
  const account = describeAccount(agent.run_as);
  const unavailable = agent.revoked_at === null && agent.reported_at !== null ? unavailableRights(t, agent) : [];
  return (
    <Card>
      <CardHeader title={t("machine.agent_title")} />
      <CardBody gap={10}>
        {agent.revoked_at !== null && <Note tone="danger">{t("machine.agent_revoked", formatWhen(agent.revoked_at, now))}</Note>}
        <KeyValue label={t("machine.field_agent")} value={agent.version} mono />
        {agent.revoked_at === null && <AgentUpdate machineID={machineID} agent={agent} updateLast={updateLast} />}
        <KeyValue label={t("machine.field_agent_enrolled")} value={formatWhen(agent.enrolled_at, now)} />
        <KeyValue label={t("machine.agent_account")} value={account ?? t("machine.agent_account_unreported")} mono={account !== null} />
        {agent.run_as?.uid === 0 && <p className="secondary">{t("machine.agent_account_root_note")}</p>}
        <KeyValue
          label={t("machine.agent_rights")}
          value={agent.reported_at === null ? t("machine.agent_rights_unreported") : agent.capabilities.map((capability) => describeCapability(t, capability)).join(" · ")}
        />
        {agent.requested.length > 0 && (
          <KeyValue label={t("machine.agent_rights_requested")} value={agent.requested.map((capability) => describeCapability(t, capability)).join(" · ")} />
        )}
        {agent.capabilities_differ && <Note tone="warn">{t("machine.agent_rights_differ")}</Note>}
        {unavailable.length > 0 && (
          <Note tone="warn">
            <strong>{t("machine.agent_rights_unavailable")}</strong>
            <ul>
              {unavailable.map((line) => (
                <li key={line}>{line}</li>
              ))}
            </ul>
          </Note>
        )}
        <p className="secondary">{t("machine.agent_rights_note")}</p>
      </CardBody>
    </Card>
  );
}

// L'onglet Disponibilité : les sondes que cette machine exécute,
// puis les certificats qu'elles ont vus, avec ce qu'il leur reste.
function MachineProbes({ machineID }: { machineID: string }) {
  const t = useT();
  const probes = useMachineProbes(machineID);
  const list = probes.data?.probes ?? [];
  return (
    <>
      {probes.error && <Failure error={probes.error} />}
      {probes.data && list.length === 0 && (
        <EmptyCard icon="globe" text={t("probes.machine_empty")} />
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
  const containers = useMachineContainers(machineID);
  const [filter, setFilter] = useState<Filter>("all");
  const all = containers.data?.containers ?? [];
  const shown = filterContainers(all, filter);
  const note = containers.data && all.length === 0 ? engineNote(t, containers.data.engines[0]) : null;
  return (
    <Card className="scroll-x">
      <CardHeader title={t("module.containers")} aside={containers.data ? t("services.machine_subtitle", all.length) : ""} />
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
      {shown.length > 0 && <ContainerTable containers={shown} />}
      {all.length > 0 && shown.length === 0 && <Empty text={t("services.empty_title")} />}
    </Card>
  );
}

// L'onglet Journaux : un raccourci. Les journaux se lisent sur la fiche
// du conteneur ; chaque ligne y mène, à l'ancre des journaux.
function MachineLogs({ machineID }: { machineID: string }) {
  const t = useT();
  const containers = useMachineContainers(machineID);
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
                  <AgentGate machineIDs={[machineID]}>
                    {(blocked) => (
                      <Button small icon="file" to={`/services/conteneurs/${container.id}#journaux`} disabled={blocked}>
                        {t("logs.open")}
                      </Button>
                    )}
                  </AgentGate>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

// Ré-enrôler (une fenêtre, aussi pour changer les droits) donne un nouveau
// jeton pour le même id et remet une machine archivée en service. Supprimer la
// machine est un seul geste : garder l'historique l'archive, sinon elle est
// effacée après confirmation sur le plan (ce qui la retient, ce qui part avec
// elle). C'est aussi un verbe : un agent peut le demander, l'opérateur confirme.
function Actions({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const [deleting, setDeleting] = useState(false);
  const archived = isArchived(machine);

  return (
    <>
      <Button icon="refresh" to={`/machines/${machine.id}/reenroller`}>
        {t("machine.reenroll")}
      </Button>
      <Button variant="danger" icon="x" onClick={() => setDeleting(true)}>
        {t("machine.delete")}
        <McpChip route="DELETE /api/machines/{id}" />
      </Button>
      {deleting && (
        <DeleteMachineDialog
          machine={machine}
          archived={archived}
          perform={(keepHistory) => deleteMachine(machine.id, keepHistory)}
          onDone={(keptHistory) => {
            setDeleting(false);
            if (!keptHistory) {
              void navigate("/machines");
            }
          }}
          onClose={() => setDeleting(false)}
        />
      )}
    </>
  );
}
