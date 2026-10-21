import { Link } from "react-router";

import { useProjects } from "../../api/directory";
import type { StatusPage, StatusPagesResponse } from "../../api/status";
import { Card, EmptyCard } from "../../components/Card";
import { Icon } from "../../components/Icon";
import { Subject, Table } from "../../components/Table";
import type { Resource } from "../../hooks/useResource";
import { useT } from "../../i18n/context";
import { pageName } from "../../lib/status";

// Les pages de statut : chacune son adresse publique, son projet s'il y en
// a un, sa langue. Un clic ouvre son formulaire ; le lien externe, la page
// telle que le visiteur la lit.
export function PagesTab({ pages }: { pages: Resource<StatusPagesResponse> }) {
  const t = useT();
  const projects = useProjects();
  const projectName = (id: string) => {
    const project = projects.data?.projects.find((candidate) => candidate.id === id);
    return project === undefined ? "" : project.title !== "" ? project.title : project.name;
  };
  const list = pages.data?.pages ?? [];
  if (pages.data !== null && list.length === 0) {
    return <EmptyCard icon="eye" title={t("statuspage.pages_empty_title")} text={t("statuspage.pages_empty_text")} />;
  }
  return (
    <Card className="scroll-x">
      <Table>
        <thead>
          <tr>
            <th>{t("statuspage.col_page")}</th>
            <th>{t("statuspage.col_address")}</th>
            <th className="wide-only">{t("statuspage.col_project")}</th>
            <th className="th-end wide-only">{t("statuspage.col_language")}</th>
          </tr>
        </thead>
        <tbody>
          {list.map((page) => (
            <PageRow key={page.id} page={page} project={page.project_id === "" ? t("statuspage.project_none") : projectName(page.project_id)} />
          ))}
        </tbody>
      </Table>
    </Card>
  );
}

function PageRow({ page, project }: { page: StatusPage; project: string }) {
  const t = useT();
  return (
    <tr>
      <td>
        <Link className="svc" to={`/page-statut/pages/${page.id}`}>
          <Subject icon="eye">{pageName(page)}</Subject>
        </Link>
      </td>
      <td>
        <a className="mono" href={page.public_url} target="_blank" rel="noopener noreferrer" title={t("statuspage.open_public")}>
          {pathOf(page.public_url)} <Icon name="external" />
        </a>
      </td>
      <td className="secondary wide-only">{project}</td>
      <td className="secondary td-end wide-only">{t(`language.${page.language}`)}</td>
    </tr>
  );
}

// L'adresse se montre sans l'hôte : c'est le chemin qui distingue les pages.
function pathOf(url: string): string {
  try {
    return new URL(url).pathname;
  } catch {
    return url;
  }
}
