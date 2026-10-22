import { useState } from "react";
import { Navigate, useNavigate, useParams } from "react-router";

import { updateAgent, useAgentUpdateOffer } from "../api/actions";
import { useMachineContainers } from "../api/containers";
import { deleteMachine, fetchMachineDeletePlan, reenrollMachine, revokeAgent, useMachines, type Agent, type Machine } from "../api/machines";
import { useMachineProbes } from "../api/probes";
import { useMachineResources } from "../api/resources";
import { ActionsCard } from "../components/ActionsCard";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, KeyValue, Note } from "../components/Card";
import { CertificateList } from "../components/Certificate";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
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
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { resolveMachineTab } from "../lib/machineTabs";
import { engineNote, filterContainers, type Filter } from "../lib/containers";
import { describeAccount, describeCapability, machineSeen, tokenPageState, unavailableRights } from "../lib/machines";
import { formatWhen } from "../lib/time";
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

function MachineView({ current, all, tab }: { current: Machine; all: Machine[]; tab: string }) {
  const t = useT();
  const now = useNow();
  const resources = useMachineResources(tab === "" ? current.id : null);
  return (
    <>
      <PageHead title={current.name} actions={<Actions machine={current} />} />
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
              <KeyValue label={t("machine.field_address")} value={current.address} mono />
              <KeyValue label={t("machine.field_os")} value={current.os} />
              <KeyValue label={t("machine.field_seen")} value={machineSeen(t, current, now)} />
              {current.lifecycle !== undefined && <SupportBlock lifecycle={current.lifecycle} subject="system" />}
            </CardBody>
          </Card>
          <AgentCard machineID={current.id} agent={current.agent} now={now} />
          <ActionsCard machineID={current.id} agent={current.agent} online={current.online} />
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

// L'agent de la machine : sa version, depuis quand, s'il est révoqué, et ses
// droits, tels qu'il les rapporte et tels que sa commande d'installation les
// demandait. Il applique ceux de son fichier ; les changer, c'est le
// ré-enrôler, par la page des droits.
function AgentCard({ machineID, agent, now }: { machineID: string; agent: Agent | null; now: number }) {
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
        {agent.revoked_at === null && <AgentUpdate machineID={machineID} />}
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
        {agent.revoked_at === null && (
          <div>
            <Button small icon="settings" to={`/machines/${machineID}/droits`}>
              {t("machine.change_rights")}
            </Button>
          </div>
        )}
      </CardBody>
    </Card>
  );
}

// La mise à jour de l'agent : le bouton quand la version du serveur est
// importée et que l'agent peut la recevoir, sinon la raison en une ligne.
// Une machine à jour n'a rien à dire.
function AgentUpdate({ machineID }: { machineID: string }) {
  const t = useT();
  const offer = useAgentUpdateOffer(machineID);
  const action = useAction();
  const current = offer.data;
  if (current === null) {
    return offer.error ? <Failure error={offer.error} /> : null;
  }
  if (current.available) {
    const update = () => {
      action.run(() => updateAgent(machineID), {
        confirm: t("machine.agent_update_confirm", current.current, current.target),
        topic: "actions",
      });
    };
    return (
      <>
        <Failure error={action.error} />
        <div>
          <Button small icon="refresh" onClick={update} disabled={action.busy}>
            {t("machine.agent_update", current.target)}
          </Button>
        </div>
        {current.reason === "right_unavailable" && <p className="secondary">{t("machine.agent_update_reason_right_unavailable")}</p>}
      </>
    );
  }
  if (current.reason === "" || current.reason === "same_version") {
    return null;
  }
  return <p className="secondary">{t(`machine.agent_update_reason_${current.reason}`, current.target, current.arch)}</p>;
}

// L'onglet Domaines et certificats : les sondes que cette machine exécute,
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

// Ré-enrôler donne un nouveau jeton pour le même id ; révoquer refuse l'agent
// et garde tout le reste ; retirer coupe l'agent et efface l'historique,
// après confirmation sur le plan : ce qui le retient (services, sondes,
// tâches, page de statut), ce qui part avec la machine.
function Actions({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const action = useAction();
  const [removing, setRemoving] = useState(false);

  const reenroll = () => {
    action.run(() => reenrollMachine(machine.id), {
      topic: "all",
      onDone: (token) => void navigate("/machines/jeton", { state: tokenPageState(token, machine.agent?.requested ?? []) }),
    });
  };

  const revoke = () => {
    action.run(() => revokeAgent(machine.id), { confirm: t("machine.revoke_confirm", machine.name), topic: "all" });
  };

  return (
    <>
      <Failure error={action.error} />
      <Button icon="refresh" onClick={reenroll}>
        {t("machine.reenroll")}
      </Button>
      {machine.agent !== null && machine.agent.revoked_at === null && (
        <Button variant="danger" icon="x" onClick={revoke}>
          {t("machine.revoke")}
        </Button>
      )}
      <Button variant="danger" icon="x" onClick={() => setRemoving(true)}>
        {t("machine.remove")}
      </Button>
      {removing && (
        <DeletePlanDialog
          question={t("machine.remove_confirm", machine.name)}
          confirmLabel={t("machine.remove")}
          loadPlan={() => fetchMachineDeletePlan(machine.id)}
          perform={() => deleteMachine(machine.id)}
          topic="all"
          onDone={() => void navigate("/machines")}
          onClose={() => setRemoving(false)}
        />
      )}
    </>
  );
}
