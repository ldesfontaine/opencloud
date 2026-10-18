import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { api, ApiError } from "../api/client";
import type { DeclaredService, GapsResponse, Project, ProjectsResponse, ServiceGap } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { NaturePill, projectNames } from "../components/DeclaredServiceTable";
import { Failure } from "../components/Failure";
import { GapPill } from "../components/GapPill";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Table } from "../components/Table";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { formatPorts, unitKeyOf } from "../lib/directory";
import { gapsPath } from "../lib/gaps";
import { useRefresh } from "../lib/refresh";
import { formatClock } from "../lib/time";
import { NotFound } from "./NotFound";

// La fiche d'un service déclaré : ses faits, ses unités, ce qu'il fournit,
// ses projets, et l'écart avec ce que sa machine montre : un état par unité
// et les conteneurs rattachés.
export function DeclaredServicePage() {
  const { id = "" } = useParams();
  const service = useResource<DeclaredService>(`/api/directory/services/${id}`);
  const projects = useResource<ProjectsResponse>("/api/directory/projects");
  // L'écart se lit pour la machine du service ; le service s'y retrouve par son id.
  const gaps = useResource<GapsResponse>(service.data ? gapsPath(service.data.machine_id, "") : null);
  if (service.error instanceof ApiError && service.error.status === 404) {
    return <NotFound />;
  }
  if (!service.data) {
    return service.error ? <Failure error={service.error} /> : null;
  }
  const gap = gaps.data?.services.find((candidate) => candidate.service_id === service.data?.id);
  return <DeclaredServiceView service={service.data} projects={projects.data?.projects ?? []} gap={gap} />;
}

function DeclaredServiceView({ service, projects, gap }: { service: DeclaredService; projects: Project[]; gap: ServiceGap | undefined }) {
  const t = useT();
  const names = projectNames(projects);
  return (
    <>
      <PageHead title={service.name} subtitle={service.title} actions={<Actions service={service} />} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <NaturePill nature={service.nature} />
            {service.needs_review && <Pill tone="warn">{t("directory.needs_review")}</Pill>}
            {gap && <GapPill state={gap.state} weak={gap.weak} />}
            <span className="meta">
              <Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>
              <span className="sep" />
              <span>{t(`directory.origin_${service.origin}`)}</span>
            </span>
          </span>
        </CardBody>
      </Card>
      <div className="grid-2">
        <Card>
          <CardHeader title={t("tab.summary")} />
          <CardBody gap={10}>
            <KeyValue label={t("directory.field_machine")} value={<Link to={`/machines/${service.machine_id}`}>{service.machine_name}</Link>} />
            <KeyValue label={t("directory.field_nature")} value={t(`directory.nature_${service.nature}`)} />
            <KeyValue label={t("directory.field_origin")} value={t(`directory.origin_${service.origin}`)} />
            {service.compose_project !== "" && <KeyValue label={t("directory.field_compose_project")} value={service.compose_project} mono />}
            {service.public_url !== "" && (
              <KeyValue
                label={t("directory.field_public_url")}
                value={
                  <a href={service.public_url} target="_blank" rel="noopener noreferrer">
                    {service.public_url}
                  </a>
                }
                mono
              />
            )}
            <KeyValue label={t("directory.field_declared_by")} value={t(`directory.declared_by_${service.declared_by}`)} />
            <KeyValue label={t("directory.field_created")} value={formatClock(service.created_at)} mono />
            <KeyValue label={t("directory.field_updated")} value={formatClock(service.updated_at)} mono />
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("directory.links_title")} aside={t("directory.links_text")} />
          <CardBody gap={10}>
            <KeyValue
              label={t("directory.field_provides")}
              value={
                service.provides.length === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  <span className="cluster">
                    {service.provides.map((capability) => (
                      <Pill key={capability} tone="accent" dot={false}>
                        {t(`directory.provides_${capability}`)}
                      </Pill>
                    ))}
                  </span>
                )
              }
            />
            <KeyValue
              label={t("directory.field_projects")}
              value={
                service.project_ids.length === 0 ? (
                  <span className="muted">—</span>
                ) : (
                  <span className="cluster">
                    {service.project_ids.map((projectID) => (
                      <Link key={projectID} to={`/services/projets/${projectID}`}>
                        {names.get(projectID) ?? projectID}
                      </Link>
                    ))}
                  </span>
                )
              }
            />
          </CardBody>
        </Card>
      </div>
      <Card className="scroll-x">
        <CardHeader title={t("directory.units_title")} aside={t("directory.units_text")} />
        {service.units.length > 0 ? (
          <Table>
            <thead>
              <tr>
                <th>{t(service.origin === "compose" ? "directory.col_compose_service" : "directory.col_unit")}</th>
                <th className="wide-only">{t("directory.col_image")}</th>
                <th>{t("directory.col_version")}</th>
                {service.origin !== "compose" && <th className="wide-only">{t(service.origin === "binary" ? "directory.col_systemd" : "directory.col_container")}</th>}
                <th className="th-end wide-only">{t("directory.col_ports")}</th>
                <th>{t("gap.column")}</th>
              </tr>
            </thead>
            <tbody>
              {service.units.map((unit) => (
                <tr key={unit.id}>
                  <td className="mono">{unit.name}</td>
                  <td className="mono wide-only">{unit.image !== "" ? unit.image : "—"}</td>
                  <td className="mono">{unit.version !== "" ? unit.version : "—"}</td>
                  {service.origin !== "compose" && <td className="mono wide-only">{unitKeyOf(unit, service.origin)}</td>}
                  <td className="mono td-end wide-only">{unit.ports.length > 0 ? formatPorts(unit.ports) : "—"}</td>
                  <td>{unitGap(gap, unit.id)}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        ) : (
          <Empty text={t("directory.units_empty")} />
        )}
      </Card>
      <AttachedContainers gap={gap} />
    </>
  );
}

// L'état d'une unité, ou un tiret tant que l'écart n'est pas lu.
function unitGap(gap: ServiceGap | undefined, unitID: string) {
  const unit = gap?.units.find((candidate) => candidate.unit_id === unitID);
  return unit ? <GapPill state={unit.state} weak={unit.weak} /> : <span className="muted">—</span>;
}

// Les conteneurs que les machines montrent pour ce service, avec le verdict
// de chacun face à son unité et, quand il n'est pas conforme, pourquoi.
function AttachedContainers({ gap }: { gap: ServiceGap | undefined }) {
  const t = useT();
  const rows = (gap?.units ?? []).flatMap((unit) => unit.containers.map((container) => ({ unit, container })));
  return (
    <Card className="scroll-x">
      <CardHeader title={t("gap.column")} />
      {rows.length > 0 ? (
        <Table>
          <thead>
            <tr>
              <th>{t("directory.col_unit")}</th>
              <th>{t("directory.col_container")}</th>
              <th className="wide-only">{t("directory.col_image")}</th>
              <th>{t("gap.column")}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ unit, container }) => (
              <tr key={container.id}>
                <td className="mono">{unit.name}</td>
                <td>
                  <Link className="mono" to={`/services/${container.id}`}>
                    {container.name}
                  </Link>
                </td>
                <td className="mono wide-only">{container.image}</td>
                <td>
                  <span className="stack">
                    <GapPill state={container.verdict} weak={container.match_level === "name"} />
                    <span className="secondary">
                      {container.reason !== "" && `${t(`gap.reason_${container.reason}`)} · `}
                      {container.match_level !== "" && t(`gap.level_${container.match_level}`)}
                    </span>
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </Table>
      ) : (
        <Empty text={t("gap.containers_none")} />
      )}
    </Card>
  );
}

// Modifier, ou supprimer après confirmation : les projets restent, seul le
// lien s'efface.
function Actions({ service }: { service: DeclaredService }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);
  const remove = () => {
    if (!window.confirm(t("directory.delete_confirm", service.name))) {
      return;
    }
    api
      .delete(`/api/directory/services/${service.id}`)
      .then(() => {
        refresh("directory");
        void navigate("/services/annuaire");
      })
      .catch(setError);
  };
  return (
    <>
      {error !== null && <Failure error={error} />}
      <Button icon="settings" to={`/services/annuaire/${service.id}/modifier`}>
        {t("directory.edit")}
      </Button>
      <Button variant="danger" icon="x" onClick={remove}>
        {t("directory.delete")}
      </Button>
    </>
  );
}
