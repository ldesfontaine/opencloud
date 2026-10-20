import { useState } from "react";

import { useContainers } from "../api/containers";
import { useMachines } from "../api/machines";
import { Card, CardBody, Empty, EmptyCard, Note } from "../components/Card";
import { DirectoryTab } from "../components/DirectoryTab";
import { Failure } from "../components/Failure";
import { MachineSelect } from "../components/MachineSelect";
import { PageHead } from "../components/PageHead";
import { ProjectsTab } from "../components/ProjectsTab";
import { ContainerFilter, ContainerTable } from "../components/ContainerTable";
import { ServicesTabs } from "../components/ServicesTabs";
import { useT } from "../i18n/context";
import { containerNeedsAttention, engineNote, filterContainers, servicesSubtitle, type Filter } from "../lib/containers";

// La page Services, sous trois onglets : l'annuaire que l'opérateur déclare
// (par défaut : c'est lui qui porte les liens), les conteneurs que Docker
// montre, et les projets.
export function Services({ tab }: { tab: "annuaire" | "conteneurs" | "projets" }) {
  if (tab === "conteneurs") {
    return <ContainersTab />;
  }
  if (tab === "projets") {
    return <ProjectsTab />;
  }
  return <DirectoryTab />;
}

// Tous les conteneurs de toutes les machines, avec un filtre par machine et
// le filtre Tous / Actifs / Arrêtés de la planche.
function ContainersTab() {
  const t = useT();
  const containers = useContainers();
  const machines = useMachines();
  const [machineID, setMachineID] = useState("");
  const [filter, setFilter] = useState<Filter>("all");
  const all = containers.data?.containers ?? [];
  const attention = all.filter(containerNeedsAttention).length;
  const byMachine = machineID === "" ? all : all.filter((container) => container.machine_id === machineID);
  const shown = filterContainers(byMachine, filter);
  const engine = containers.data?.engines.find((candidate) => candidate.machine_id === machineID);
  const note = machineID !== "" && byMachine.length === 0 && containers.data ? engineNote(t, engine) : null;
  return (
    <>
      <PageHead
        title={t("nav.services")}
        subtitle={containers.data ? servicesSubtitle(t, all.length, attention) : ""}
        actions={
          <MachineSelect
            icon="server"
            aria-label={t("services.col_machine")}
            machines={machines.data?.machines ?? []}
            value={machineID}
            onChange={setMachineID}
            noneLabel={t("services.all_machines")}
          />
        }
      />
      <ServicesTabs />
      {containers.error && <Failure error={containers.error} />}
      {containers.data && all.length === 0 && (
        <EmptyCard icon="box" title={t("services.empty_title")} text={t("services.empty_text")} />
      )}
      {containers.data && all.length > 0 && (
        <Card className="scroll-x">
          <div className="table-bar">
            <ContainerFilter value={filter} onChange={setFilter} />
            {machineID !== "" && <span className="secondary">{t("services.machine_subtitle", byMachine.length)}</span>}
          </div>
          {note !== null && (
            <CardBody>
              <Note tone="warn">{note}</Note>
            </CardBody>
          )}
          {shown.length > 0 ? <ContainerTable containers={shown} withMachine /> : <Empty text={t("services.empty_title")} />}
        </Card>
      )}
    </>
  );
}
