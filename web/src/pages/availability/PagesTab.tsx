import { Link } from "react-router";

import { useProjects, type Project } from "../../api/directory";
import { useStatusComponents, type StatusComponent } from "../../api/status";
import { Card, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Icon } from "../../components/Icon";
import { Subject, Table } from "../../components/Table";
import { useT } from "../../i18n/context";
import { projectLabel, publicAddressOf } from "../../lib/projects";
import { publicPagePath, worstState } from "../../lib/status";
import { StatusStatePill } from "./StatusStatePill";

// Les pages publiques : un projet publié, une page, à son adresse. Un clic
// ouvre ses composants ; le lien externe, la page telle que le visiteur la lit.
// La page se publie, se modifie et se retire depuis le projet (Services).
export function PagesTab() {
  const t = useT();
  const projects = useProjects();
  const components = useStatusComponents(null);
  const published = (projects.data?.projects ?? []).filter((project) => project.publication !== null);
  return (
    <>
      {projects.error && <Failure error={projects.error} />}
      {components.error && <Failure error={components.error} />}
      {projects.data && published.length === 0 && <EmptyCard icon="eye" title={t("availability.pages_empty_title")} text={t("availability.pages_empty_text")} />}
      {projects.data && published.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("availability.col_page")}</th>
                <th>{t("availability.col_address")}</th>
                <th className="th-num wide-only">{t("availability.col_components")}</th>
                <th className="th-end">{t("availability.col_shown")}</th>
              </tr>
            </thead>
            <tbody>
              {published.map((project) => (
                <PageRow key={project.id} project={project} components={(components.data?.components ?? []).filter((component) => component.project_id === project.id)} />
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}

function PageRow({ project, components }: { project: Project; components: StatusComponent[] }) {
  const t = useT();
  const publication = project.publication;
  if (publication === null) {
    return null;
  }
  return (
    <tr>
      <td>
        <Link className="svc" to={publicPagePath(project.id)}>
          <Subject icon="eye">{projectLabel(project)}</Subject>
        </Link>
      </td>
      <td>
        <a className="mono" href={publication.public_url} target="_blank" rel="noopener noreferrer" title={t("availability.open_public")}>
          {publicAddressOf(publication.slug)} <Icon name="external" />
        </a>
      </td>
      <td className="num td-num wide-only">{components.length}</td>
      <td className="td-end">
        <StatusStatePill state={worstState(components.map((component) => component.effective))} />
      </td>
    </tr>
  );
}
