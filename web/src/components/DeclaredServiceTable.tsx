import { Link } from "react-router";

import type { DeclaredService, Project } from "../api/types";
import { useT } from "../i18n/context";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// Un nom de projet par identifiant, pour dire à quels projets appartient
// chaque service sans relire la liste ligne par ligne.
export function projectNames(projects: Project[]): Map<string, string> {
  return new Map(projects.map((project) => [project.id, project.name]));
}

// La nature en pastille : un outil d'infrastructure se distingue d'une
// application au premier coup d'œil.
export function NaturePill({ nature }: { nature: DeclaredService["nature"] }) {
  const t = useT();
  return <Pill tone={nature === "infra" ? "accent" : "neutral"}>{t(`directory.nature_${nature}`)}</Pill>;
}

// Le tableau des services déclarés : nom et titre, machine, nature,
// origine, nombre d'unités, projets, et la pastille « à vérifier ».
export function DeclaredServiceTable({ services, projects }: { services: DeclaredService[]; projects: Project[] }) {
  const t = useT();
  const names = projectNames(projects);
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("directory.col_service")}</th>
          <th className="wide-only">{t("directory.col_machine")}</th>
          <th>{t("directory.col_nature")}</th>
          <th className="wide-only">{t("directory.col_origin")}</th>
          <th className="th-num wide-only">{t("directory.col_units")}</th>
          <th className="th-end wide-only">{t("directory.col_projects")}</th>
        </tr>
      </thead>
      <tbody>
        {services.map((service) => (
          <tr key={service.id}>
            <td>
              <Link className="svc" to={`/services/annuaire/${service.id}`}>
                <Subject icon="box">
                  <span className="svc-name">
                    <span>{service.name}</span>
                    {service.title !== "" && <span className="mono muted">{service.title}</span>}
                  </span>
                </Subject>
              </Link>
              {service.needs_review && (
                <Pill tone="warn" dot={false}>
                  {t("directory.needs_review")}
                </Pill>
              )}
            </td>
            <td className="wide-only">
              <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
            </td>
            <td>
              <NaturePill nature={service.nature} />
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
        ))}
      </tbody>
    </Table>
  );
}
