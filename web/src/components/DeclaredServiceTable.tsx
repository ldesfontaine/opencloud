import { Link } from "react-router";

import type { DeclaredService, ServiceGap, Project } from "../api/directory";
import { useT } from "../i18n/context";
import { projectNames } from "../lib/directory";
import { GapPill } from "./GapPill";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

// La nature en pastille : un outil d'infrastructure se distingue d'une
// application au premier coup d'œil.
export function NaturePill({ nature }: { nature: DeclaredService["nature"] }) {
  const t = useT();
  return <Pill tone={nature === "infra" ? "accent" : "neutral"}>{t(`directory.nature_${nature}`)}</Pill>;
}

// Le tableau des services déclarés : nom et titre, machine, nature,
// origine, nombre d'unités, projets et l'écart.
// Sans réponse sur l'écart (elle se lit à part), la colonne reste vide.
export function DeclaredServiceTable({
  services,
  projects,
  gaps,
}: {
  services: DeclaredService[];
  projects: Project[];
  gaps: Map<string, ServiceGap>;
}) {
  const t = useT();
  const names = projectNames(projects);
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("directory.col_service")}</th>
          <th className="wide-only">{t("directory.col_machine")}</th>
          <th>{t("directory.col_nature")}</th>
          <th>{t("gap.column")}</th>
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
            </td>
            <td className="wide-only">
              <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
            </td>
            <td>
              <NaturePill nature={service.nature} />
            </td>
            <td>
              <GapCell gap={gaps.get(service.id)} />
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

function GapCell({ gap }: { gap: ServiceGap | undefined }) {
  return gap ? <GapPill state={gap.state} weak={gap.weak} observed={gap.observed} /> : <span className="muted">—</span>;
}
