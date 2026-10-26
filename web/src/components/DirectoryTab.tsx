import { useState } from "react";

import { useDeclaredServices, useGaps, useProjects } from "../api/directory";
import { useMachines } from "../api/machines";
import { manifestExportPath } from "../api/manifest";
import { useT } from "../i18n/context";
import { directorySubtitle } from "../lib/directory";
import { gapsByService } from "../lib/gaps";
import { Button } from "./Button";
import { Card, Empty, EmptyCard } from "./Card";
import { DeclaredServiceTable } from "./DeclaredServiceTable";
import { Failure } from "./Failure";
import { MachineSelect } from "./MachineSelect";
import { Select } from "./Select";
import { UndeclaredList } from "./UndeclaredList";
import { PageHead } from "./PageHead";
import { ServicesTabs } from "./ServicesTabs";

// L'onglet Annuaire : ce que l'opérateur déclare, filtré par machine et par
// projet, avec l'écart avec ce que les machines montrent, puis ce qui
// tourne sans être déclaré. La liste se relit en entier et se filtre ici,
// pour que le sous-titre compte tout ce qui est déclaré ; l'écart, lui, se
// lit avec le même filtre, le serveur sachant quels conteneurs un projet
// exclut.
export function DirectoryTab() {
  const t = useT();
  const services = useDeclaredServices();
  const projects = useProjects();
  const machines = useMachines();
  const [machineID, setMachineID] = useState("");
  const [projectID, setProjectID] = useState("");
  const gaps = useGaps({ machineID, projectID });
  const all = services.data?.services ?? [];
  const shown = all.filter(
    (service) => (machineID === "" || service.machine_id === machineID) && (projectID === "" || service.project_ids.includes(projectID)),
  );
  return (
    <>
      <PageHead
        title={t("nav.services")}
        subtitle={services.data ? directorySubtitle(t, all.length) : ""}
        actions={
          <>
            <Button icon="clock" to="/services/annuaire/journal">
              {t("manifest.journal")}
            </Button>
            <Button icon="file" href={manifestExportPath} download="">
              {t("manifest.export")}
            </Button>
            <Button variant="primary" icon="plus" to="/services/annuaire/nouveau">
              {t("directory.add")}
            </Button>
          </>
        }
      />
      <ServicesTabs />
      {services.error && <Failure error={services.error} />}
      {services.data && all.length === 0 && (
        <EmptyCard icon="box" title={t("directory.empty_title")} text={t("directory.empty_text")} />
      )}
      {services.data && all.length > 0 && (
        <Card className="scroll-x">
          <div className="table-bar">
            <span className="cluster">
              <MachineSelect
                icon="server"
                aria-label={t("directory.col_machine")}
                machines={machines.data?.machines ?? []}
                value={machineID}
                onChange={setMachineID}
                noneLabel={t("services.all_machines")}
              />
              <Select
                aria-label={t("directory.col_projects")}
                value={projectID}
                options={[{ value: "", label: t("directory.all_projects") }, ...(projects.data?.projects ?? []).map((project) => ({ value: project.id, label: project.name }))]}
                onChange={setProjectID}
              />
            </span>
          </div>
          {shown.length > 0 ? (
            <DeclaredServiceTable services={shown} projects={projects.data?.projects ?? []} gaps={gapsByService(gaps.data)} />
          ) : (
            <Empty text={t("directory.empty_filtered")} />
          )}
        </Card>
      )}
      {gaps.error && <Failure error={gaps.error} />}
      {/* Un projet ne regroupe que du déclaré : il n'a pas de non-déclarés à dire. */}
      {gaps.data && projectID === "" && <UndeclaredList groups={gaps.data.undeclared} declared={all} />}
    </>
  );
}
