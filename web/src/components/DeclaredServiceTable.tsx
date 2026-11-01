import { Fragment, useState } from "react";
import { Link } from "react-router";

import type { Container } from "../api/containers";
import type { DeclaredService, ServiceGap, Project } from "../api/directory";
import { useT } from "../i18n/context";
import { containerNeedsAttention } from "../lib/containers";
import { projectNames, toggled } from "../lib/directory";
import { groupServices, worstGap, type InstanceGroup } from "../lib/serviceGroups";
import { CategoryPill } from "./CategoryPill";
import { ContainerTable } from "./ContainerTable";
import { GapPill } from "./GapPill";
import { Icon } from "./Icon";
import { Pill } from "./Pill";
import { Subject, Table } from "./Table";
import "./ServiceTable.scss";

const columnCount = 8;

// Le tableau des services déclarés : une ligne par service (traefik,
// authelia, crowdsec), rangées sous leur projet Compose, le groupe
// d'exécution que la machine montre. Le même service sur plusieurs machines
// (même nom, même image) est une seule ligne qui se déplie en une ligne par
// machine. Les conteneurs d'un service se déplient sous sa ligne ; ceux
// qu'aucun service ne porte sont sous « Propositions », plus haut.
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
  // Un seul registre de lignes dépliées : l'identifiant d'un service déplie ses
  // conteneurs, « group: » et la clé d'un groupe déplient ses machines.
  const [unfolded, setUnfolded] = useState<string[]>([]);
  const toggle = (key: string) => setUnfolded((current) => toggled(current, key));
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("directory.col_service")}</th>
          <th className="wide-only">{t("directory.col_machine")}</th>
          <th>{t("directory.col_category")}</th>
          <th>{t("gap.column")}</th>
          <th>{t("services.col_containers")}</th>
          <th className="wide-only">{t("directory.col_origin")}</th>
          <th className="th-num wide-only">{t("directory.col_units")}</th>
          <th className="th-end wide-only">{t("directory.col_projects")}</th>
        </tr>
      </thead>
      <tbody>
        {groupServices(services).map((section) => (
          <Fragment key={section.project}>
            <tr className="svc-project">
              <th colSpan={columnCount} scope="colgroup">
                <span className="cluster">
                  <Icon name="box" />
                  {section.project === "" ? t("services.group_none") : t("services.group_project", section.project)}
                </span>
              </th>
            </tr>
            {section.groups.map((group) => (
              <GroupRows key={group.key} group={group} names={names} gaps={gaps} containers={containers} unfolded={unfolded} onToggle={toggle} />
            ))}
          </Fragment>
        ))}
      </tbody>
    </Table>
  );
}

// Un service sur une seule machine est une ligne de service ; sur plusieurs,
// une ligne de groupe.
function GroupRows({
  group,
  names,
  gaps,
  containers,
  unfolded,
  onToggle,
}: {
  group: InstanceGroup;
  names: Map<string, string>;
  gaps: Map<string, ServiceGap>;
  containers: Map<string, Container[]>;
  unfolded: string[];
  onToggle: (key: string) => void;
}) {
  const [only] = group.instances;
  if (group.instances.length === 1 && only !== undefined) {
    return (
      <ServiceRows service={only} names={names} gap={gaps.get(only.id)} carried={containers.get(only.id) ?? []} unfolded={unfolded.includes(only.id)} onToggle={() => onToggle(only.id)} />
    );
  }
  return <InstanceRows group={group} names={names} gaps={gaps} containers={containers} unfolded={unfolded} onToggle={onToggle} />;
}

// Un service sur plusieurs machines : une ligne pour le service, qui dit le
// pire écart de ses instances, et, dépliée, une ligne par machine.
function InstanceRows({
  group,
  names,
  gaps,
  containers,
  unfolded,
  onToggle,
}: {
  group: InstanceGroup;
  names: Map<string, string>;
  gaps: Map<string, ServiceGap>;
  containers: Map<string, Container[]>;
  unfolded: string[];
  onToggle: (key: string) => void;
}) {
  const t = useT();
  const key = `group:${group.key}`;
  const open = unfolded.includes(key);
  const [first] = group.instances;
  if (first === undefined) {
    return null;
  }
  const carried = group.instances.reduce((sum, instance) => sum + (containers.get(instance.id)?.length ?? 0), 0);
  const gap = worstGap(group.instances, gaps);
  return (
    <>
      <tr className="svc-group">
        <td>
          <Subject icon="box">
            <span className="svc-name">
              <span>{group.name}</span>
            </span>
          </Subject>
        </td>
        <td className="wide-only">
          <button
            type="button"
            className={open ? "svc-toggle unfolded" : "svc-toggle"}
            aria-expanded={open}
            aria-label={t(open ? "services.instances_collapse" : "services.instances_expand", group.name)}
            onClick={() => onToggle(key)}
          >
            <Icon name="chevron-right" />
            {t("services.instances_count", group.instances.length)}
          </button>
        </td>
        <td>
          <CategoryPill service={first} />
        </td>
        <td>
          <GapCell gap={gap} />
        </td>
        <td>{carried === 0 ? <span className="muted">—</span> : <span className="num">{carried}</span>}</td>
        <td className="wide-only">{t(`directory.origin_${first.origin}`)}</td>
        <td className="num td-num wide-only">{first.units.length}</td>
        <td className="td-end wide-only">
          <span className="muted">—</span>
        </td>
      </tr>
      {open &&
        group.instances.map((instance) => (
          <ServiceRows
            key={instance.id}
            service={instance}
            names={names}
            gap={gaps.get(instance.id)}
            carried={containers.get(instance.id) ?? []}
            unfolded={unfolded.includes(instance.id)}
            onToggle={() => onToggle(instance.id)}
            isInstance
          />
        ))}
    </>
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
  isInstance = false,
}: {
  service: DeclaredService;
  names: Map<string, string>;
  gap: ServiceGap | undefined;
  carried: Container[];
  unfolded: boolean;
  onToggle: () => void;
  isInstance?: boolean;
}) {
  const t = useT();
  return (
    <>
      <tr className={isInstance ? "svc-instance" : undefined}>
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
          <CategoryPill service={service} />
        </td>
        <td>
          <GapCell gap={gap} />
        </td>
        <td>
          <ContainersToggle service={service} carried={carried} unfolded={unfolded} onToggle={onToggle} />
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
          <td colSpan={columnCount}>
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
