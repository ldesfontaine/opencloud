import { useState } from "react";
import { Link } from "react-router";

import type { Container } from "../api/containers";
import type { DeclaredService, ServiceGap, Project } from "../api/directory";
import { useT } from "../i18n/context";
import { containerNeedsAttention } from "../lib/containers";
import { projectNames, toggled } from "../lib/directory";
import { ContainerTable } from "./ContainerTable";
import { GapPill } from "./GapPill";
import { Icon } from "./Icon";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// La nature en pastille : un outil d'infrastructure se distingue d'une
// application au premier coup d'œil.
export function NaturePill({ nature }: { nature: DeclaredService["nature"] }) {
  const t = useT();
  return <Pill tone={nature === "infra" ? "accent" : "neutral"}>{t(`directory.nature_${nature}`)}</Pill>;
}

// Le tableau des services déclarés : nom et titre, machine, nature, l'écart,
// les conteneurs qui le portent, origine, nombre d'unités et projets. Les
// conteneurs d'un service se déplient sous sa ligne ; ceux qu'aucun service
// ne porte sont sous « Non déclarés », plus bas.
// Sans réponse sur l'écart (elle se lit à part), la colonne reste vide.
export function DeclaredServiceTable({
  services,
  projects,
  gaps,
  containers,
}: {
  services: DeclaredService[];
  projects: Project[];
  gaps: Map<string, ServiceGap>;
  containers: Map<string, Container[]>;
}) {
  const t = useT();
  const names = projectNames(projects);
  const [unfolded, setUnfolded] = useState<string[]>([]);
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("directory.col_service")}</th>
          <th className="wide-only">{t("directory.col_machine")}</th>
          <th>{t("directory.col_nature")}</th>
          <th>{t("gap.column")}</th>
          <th>{t("services.col_containers")}</th>
          <th className="wide-only">{t("directory.col_origin")}</th>
          <th className="th-num wide-only">{t("directory.col_units")}</th>
          <th className="th-end wide-only">{t("directory.col_projects")}</th>
        </tr>
      </thead>
      <tbody>
        {services.map((service) => (
          <ServiceRows
            key={service.id}
            service={service}
            names={names}
            gap={gaps.get(service.id)}
            carried={containers.get(service.id) ?? []}
            unfolded={unfolded.includes(service.id)}
            onToggle={() => setUnfolded((current) => toggled(current, service.id))}
          />
        ))}
      </tbody>
    </Table>
  );
}

// Une ligne de service et, dépliée, la liste de ses conteneurs juste dessous.
function ServiceRows({
  service,
  names,
  gap,
  carried,
  unfolded,
  onToggle,
}: {
  service: DeclaredService;
  names: Map<string, string>;
  gap: ServiceGap | undefined;
  carried: Container[];
  unfolded: boolean;
  onToggle: () => void;
}) {
  const t = useT();
  return (
    <>
      <tr>
        <td>
          <Link className="svc" to={`/services/annuaire/${service.id}`}>
            <Subject icon="box">
              <span className="svc-name">
                <span>{service.name}</span>
                {service.title !== "" && <span className="mono muted">{service.title}</span>}
              </span>
            </Subject>
          </Link>
        </td>
        <td className="wide-only">
          <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
        </td>
        <td>
          <NaturePill nature={service.nature} />
        </td>
        <td>
          <GapCell gap={gap} />
        </td>
        <td>
          <ContainersToggle
            service={service}
            carried={carried}
            unfolded={unfolded}
            onToggle={onToggle}
          />
        </td>
        <td className="wide-only">{t(`directory.origin_${service.origin}`)}</td>
        <td className="num td-num wide-only">{service.units.length}</td>
        <td className="td-end wide-only">
          {service.project_ids.length === 0 ? (
            <span className="muted">—</span>
          ) : (
            <span className="cluster">
              {service.project_ids.map((projectID) => (
                <Link key={projectID} to={`/services/projets/${projectID}`}>
                  {names.get(projectID) ?? projectID}
                </Link>
              ))}
            </span>
          )}
        </td>
      </tr>
      {unfolded && carried.length > 0 && (
        <tr className="svc-containers">
          <td colSpan={8}>
            <ContainerTable containers={carried} />
          </td>
        </tr>
      )}
    </>
  );
}

function GapCell({ gap }: { gap: ServiceGap | undefined }) {
  return gap ? <GapPill state={gap.state} weak={gap.weak} observed={gap.observed} /> : <span className="muted">—</span>;
}

// Le nombre de conteneurs du service, qui les déplie ou les replie ; ce qui
// demande attention parmi eux se dit sans qu'on déplie. Sans conteneur, un tiret.
function ContainersToggle({ service, carried, unfolded, onToggle }: { service: DeclaredService; carried: Container[]; unfolded: boolean; onToggle: () => void }) {
  const t = useT();
  if (carried.length === 0) {
    return <span className="muted">—</span>;
  }
  const attention = carried.filter(containerNeedsAttention).length;
  return (
    <span className="cluster">
      <button
        type="button"
        className={unfolded ? "svc-toggle unfolded" : "svc-toggle"}
        aria-expanded={unfolded}
        aria-label={t(unfolded ? "services.containers_collapse" : "services.containers_expand", service.name)}
        onClick={onToggle}
      >
        <Icon name="chevron-right" />
        {carried.length === 1 ? t("services.containers_one") : t("services.containers_count", carried.length)}
      </button>
      {attention > 0 && (
        <Pill tone="warn" small>
          {t("services.containers_attention", attention)}
        </Pill>
      )}
    </span>
  );
}
