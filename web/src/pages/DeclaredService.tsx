import { useState } from "react";
import { Link, Outlet, useNavigate, useParams } from "react-router";

import { useAlerts } from "../api/alerts";
import {
  deleteDeclaredService,
  fetchDeclaredServiceDeletePlan,
  useDeclaredService,
  useGaps,
  useProjects,
  type DeclaredServiceDetail,
  type LinkedObject,
  type ObservedUnit,
  type Project,
  type ServiceGap,
  type ServiceLinks,
} from "../api/directory";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { DeployCard } from "../components/DeployCard";
import { ImageName } from "../components/ImageName";
import { CategoryPill, shownCategory } from "../components/CategoryPill";
import { DeletePlanDialog } from "../components/DeletePlanDialog";
import { Loaded } from "../components/Loaded";
import { GapPill } from "../components/GapPill";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { List, Table } from "../components/Table";
import { useT } from "../i18n/context";
import { formatUnitPorts, hasLinks, projectNames, unitKeyOf } from "../lib/directory";
import { needsPort, observedPill } from "../lib/systemd";
import { newMutePath } from "../lib/alerts";
import { componentLookupPath } from "../lib/status";
import { probePath } from "../lib/probes";
import { formatClock } from "../lib/time";
import { AlertRow } from "./alerts/AlertRow";

// La fiche d'un service déclaré : ses faits, ses unités, ce qu'il fournit,
// ses projets, ce qui le lie (sondes et composants de statut), ses alertes
// ouvertes (celles de ses conteneurs et de ses sondes), et l'écart
// avec ce que sa machine montre : un état par unité et les conteneurs
// rattachés.
export function DeclaredServicePage() {
  const { id = "" } = useParams();
  const service = useDeclaredService(id);
  const projects = useProjects();
  // L'écart se lit pour la machine du service ; le service s'y retrouve par son id.
  const gaps = useGaps(service.data ? { machineID: service.data.machine_id, projectID: "" } : null);
  return (
    <Loaded resource={service}>
      {(data) => (
        <DeclaredServiceView service={data} projects={projects.data?.projects ?? []} gap={gaps.data?.services.find((candidate) => candidate.service_id === data.id)} />
      )}
    </Loaded>
  );
}

function DeclaredServiceView({ service, projects, gap }: { service: DeclaredServiceDetail; projects: Project[]; gap: ServiceGap | undefined }) {
  const t = useT();
  const names = projectNames(projects);
  return (
    <>
      <PageHead title={service.name} subtitle={service.title} actions={<Actions service={service} />} />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <CategoryPill service={service} />
            {gap && <GapPill state={gap.state} weak={gap.weak} observed={gap.observed} />}
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
            <KeyValue
              label={t("directory.field_category")}
              value={
                <>
                  {t(`directory.category_${shownCategory(service)}`)}
                  {service.category === "" && <span className="muted"> · {t("directory.category_deduced_note")}</span>}
                </>
              }
            />
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
      <LinksCard links={service.links} />
      <AlertsCard serviceID={service.id} />
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
                {service.origin === "binary" && <th>{t("directory.col_unit_state")}</th>}
                <th>{t("directory.update_policy_label")}</th>
                <th>{t("gap.column")}</th>
              </tr>
            </thead>
            <tbody>
              {service.units.map((unit) => (
                <tr key={unit.id}>
                  <td className="mono">{unit.name}</td>
                  <td className="mono wide-only">{unit.image !== "" ? <ImageName image={unit.image} /> : "—"}</td>
                  <td className="mono">{unit.version !== "" ? unit.version : "—"}</td>
                  {service.origin !== "compose" && <td className="mono wide-only">{unitKeyOf(unit, service.origin)}</td>}
                  <td className="mono td-end wide-only">{unit.ports.length > 0 ? formatUnitPorts(unit.ports) : needsPort(unit.kind, unit.ports) ? <PortToDeclare serviceID={service.id} /> : "—"}</td>
                  {service.origin === "binary" && <td>{unit.observed !== null ? <ObservedState observed={unit.observed} /> : <span className="muted">—</span>}</td>}
                  <td>{unit.kind === "container" ? t(`update.policy_${unit.update_policy}`) : <span className="muted">—</span>}</td>
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
      {service.origin === "compose" && <DeployCard serviceID={service.id} />}
      {/* La fenêtre de l'adresse : modifier le service. */}
      <Outlet />
    </>
  );
}

// Un binaire sans port : le relevé systemd ne dit pas où il écoute. Le lien
// ouvre la modification directement sur l'étape des unités.
function PortToDeclare({ serviceID }: { serviceID: string }) {
  const t = useT();
  return (
    <span className="stack" title={t("directory.port_to_declare_help")}>
      <span className="muted">{t("directory.port_to_declare")}</span>
      <Link to={`/services/annuaire/${serviceID}/modifier?etape=units`}>{t("directory.port_declare_link")}</Link>
    </span>
  );
}

// L'état d'une unité binaire d'après le relevé de sa machine : en marche, en
// échec, absente de la machine, ou pas encore relevée. Une absence dit quoi
// vérifier.
function ObservedState({ observed }: { observed: ObservedUnit }) {
  const t = useT();
  const pill = observedPill(observed);
  const help = observed.presence === "missing" ? t("systemd.presence_missing_help") : observed.presence === "unknown" ? t("systemd.presence_unknown_help") : undefined;
  return (
    <span className="cluster" title={help}>
      <Pill tone={pill.tone}>{t(pill.key)}</Pill>
      {observed.presence === "found" && <span className="secondary">{t(`systemd.sub_${observed.sub}`)}</span>}
    </span>
  );
}

// L'état d'une unité, ou un tiret tant que l'écart n'est pas lu.
function unitGap(gap: ServiceGap | undefined, unitID: string) {
  const unit = gap?.units.find((candidate) => candidate.unit_id === unitID);
  return unit ? <GapPill state={unit.state} weak={unit.weak} observed={gap?.observed ?? true} /> : <span className="muted">—</span>;
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
                  <Link className="mono" to={`/services/conteneurs/${container.id}`}>
                    {container.name}
                  </Link>
                </td>
                <td className="mono wide-only">
                  <ImageName image={container.image} />
                </td>
                <td>
                  <span className="stack">
                    <GapPill state={container.verdict} weak={container.match_level === "name"} observed={gap?.observed ?? true} />
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

// Ce qui vise ce service : tant qu'une sonde ou un composant de statut le
// vise, il ne se supprime pas.
// Les alertes ouvertes qui portent ce service ; le silence se pose d'ici
// sur le service entier.
function AlertsCard({ serviceID }: { serviceID: string }) {
  const t = useT();
  const alerts = useAlerts();
  const mine = (alerts.data?.alerts ?? []).filter((alert) => alert.service_id === serviceID);
  const mute = (
    <Button small variant="ghost" icon="pause" to={`${newMutePath}?target_kind=service&target_id=${encodeURIComponent(serviceID)}`}>
      {t("alerts.silence")}
    </Button>
  );
  return (
    <Card>
      <CardHeader title={t("directory.alerts_title")} aside={mute} />
      {mine.length === 0 ? (
        <CardBody>
          <span className="secondary">{t("directory.alerts_none")}</span>
        </CardBody>
      ) : (
        <List>
          {mine.map((alert) => (
            <AlertRow key={alert.id} alert={alert} compact />
          ))}
        </List>
      )}
    </Card>
  );
}

function LinksCard({ links }: { links: ServiceLinks }) {
  const t = useT();
  return (
    <Card>
      <CardHeader title={t("directory.bound_title")} />
      <CardBody gap={10}>
        {!hasLinks(links) ? (
          <span className="secondary">{t("directory.links_none")}</span>
        ) : (
          <>
            <LinkList label={t("directory.links_probes")} objects={links.probes} to={(object) => probePath(object.id)} />
            <LinkList label={t("directory.links_components")} objects={links.components} to={(object) => componentLookupPath(object.id)} />
          </>
        )}
      </CardBody>
    </Card>
  );
}

function LinkList({ label, objects, to }: { label: string; objects: LinkedObject[]; to: (object: LinkedObject) => string }) {
  if (objects.length === 0) {
    return null;
  }
  return (
    <KeyValue
      label={label}
      value={
        <span className="cluster">
          {objects.map((object) => (
            <Link key={object.id} to={to(object)}>
              {object.name}
            </Link>
          ))}
        </span>
      }
    />
  );
}

// Modifier, ou supprimer après confirmation sur le plan : les projets
// restent, seul le lien s'efface. Un service que des sondes ou des
// composants visent ne se supprime pas : le plan les nomme, et un 409 reçu
// quand même (le lien est apparu entre-temps) montre le plan du serveur.
// Ses conteneurs qui tournent reviennent en proposition ; « Supprimer et
// ignorer » les dit voulus du même geste (pas pour un binaire, qui n'a pas de
// conteneur).
function Actions({ service }: { service: DeclaredServiceDetail }) {
  const t = useT();
  const navigate = useNavigate();
  const [removing, setRemoving] = useState<"delete" | "ignore" | null>(null);
  const ignoring = removing === "ignore";
  return (
    <>
      <Button icon="settings" to={`/services/annuaire/${service.id}/modifier`}>
        {t("directory.edit")}
      </Button>
      {service.origin !== "binary" && (
        <Button variant="danger" icon="x" onClick={() => setRemoving("ignore")}>
          {t("directory.delete_ignore")}
        </Button>
      )}
      <Button variant="danger" icon="x" onClick={() => setRemoving("delete")}>
        {t("directory.delete")}
      </Button>
      {removing !== null && (
        <DeletePlanDialog
          question={t(ignoring ? "directory.delete_ignore_confirm" : "directory.delete_confirm", service.name)}
          confirmLabel={t(ignoring ? "directory.delete_ignore" : "directory.delete")}
          loadPlan={() => fetchDeclaredServiceDeletePlan(service.id)}
          perform={() => deleteDeclaredService(service.id, ignoring)}
          topic="directory"
          onDone={() => void navigate("/services")}
          onClose={() => setRemoving(null)}
        />
      )}
    </>
  );
}
