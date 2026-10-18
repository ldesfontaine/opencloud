import { useState } from "react";

import type { MachinesResponse, ServicesResponse } from "../api/types";
import { Card, CardBody, Empty, Note } from "../components/Card";
import { DirectoryTab } from "../components/DirectoryTab";
import { Failure } from "../components/Failure";
import { Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { ProjectsTab } from "../components/ProjectsTab";
import { filterServices, ServiceFilter, ServiceTable, useServiceFilter } from "../components/ServiceTable";
import { ServicesTabs } from "../components/ServicesTabs";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { engineNote, needsAttention, servicesSubtitle } from "../lib/services";

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
  const services = useResource<ServicesResponse>("/api/services");
  const machines = useResource<MachinesResponse>("/api/machines");
  const [machineID, setMachineID] = useState("");
  const [filter, setFilter] = useServiceFilter();
  const all = services.data?.services ?? [];
  const attention = all.filter(needsAttention).length;
  const byMachine = machineID === "" ? all : all.filter((service) => service.machine_id === machineID);
  const shown = filterServices(byMachine, filter);
  const engine = services.data?.engines.find((candidate) => candidate.machine_id === machineID);
  const note = machineID !== "" && byMachine.length === 0 && services.data ? engineNote(t, engine) : null;
  return (
    <>
      <PageHead
        title={t("nav.services")}
        subtitle={services.data ? servicesSubtitle(t, all.length, attention) : ""}
        actions={
          <Select icon="server" aria-label={t("services.col_machine")} value={machineID} onChange={(event) => setMachineID(event.target.value)}>
            <option value="">{t("services.all_machines")}</option>
            {(machines.data?.machines ?? []).map((machine) => (
              <option key={machine.id} value={machine.id}>
                {machine.name}
              </option>
            ))}
          </Select>
        }
      />
      <ServicesTabs />
      {services.error && <Failure error={services.error} />}
      {services.data && all.length === 0 && (
        <Card>
          <Empty icon="box" title={t("services.empty_title")} text={t("services.empty_text")} />
        </Card>
      )}
      {services.data && all.length > 0 && (
        <Card className="scroll-x">
          <div className="table-bar">
            <ServiceFilter value={filter} onChange={setFilter} />
            {machineID !== "" && <span className="secondary">{t("services.machine_subtitle", byMachine.length)}</span>}
          </div>
          {note !== null && (
            <CardBody>
              <Note tone="warn">{note}</Note>
            </CardBody>
          )}
          {shown.length > 0 ? <ServiceTable services={shown} withMachine /> : <Empty text={t("services.empty_title")} />}
        </Card>
      )}
    </>
  );
}
