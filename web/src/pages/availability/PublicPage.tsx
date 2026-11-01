import { Link, useParams } from "react-router";

import { useProject } from "../../api/directory";
import { useStatusComponents, type StatusComponent } from "../../api/status";
import { ActionButton } from "../../components/ActionButton";
import { Button } from "../../components/Button";
import { Card, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Icon } from "../../components/Icon";
import { Loaded } from "../../components/Loaded";
import { PageHead } from "../../components/PageHead";
import { Pill } from "../../components/Pill";
import { Subject, Table } from "../../components/Table";
import { useT } from "../../i18n/context";
import { projectLabel, publicAddressOf } from "../../lib/projects";
import { componentPath, newComponentPath, publicPagesPath } from "../../lib/status";
import { NotFound } from "../NotFound";
import { StatusStatePill } from "./StatusStatePill";

// La page publique d'un projet : son adresse, son état d'ensemble, et ses
// composants avec les objets qui les font vivre (ce que le visiteur ne voit
// jamais). Sans composant saisi, la page montre un composant par service du
// projet : ils se lisent ici, marqués automatiques. Saisir un premier composant
// les remplace tous.
export function PublicPage() {
  const { id = "" } = useParams();
  const t = useT();
  const project = useProject(id);
  const components = useStatusComponents(id);
  if (project.error?.status === 404) {
    return <NotFound />;
  }
  return (
    <Loaded resource={project}>
      {(found) => (
        <>
          <PageHead
            title={projectLabel(found)}
            subtitle={found.publication === null ? t("availability.page_unpublished") : t("availability.page_subtitle", publicAddressOf(found.publication.slug))}
            actions={
              <>
                {found.publication !== null && (
                  <a className="btn btn-secondary" href={found.publication.public_url} target="_blank" rel="noopener noreferrer">
                    <Icon name="external" />
                    {t("availability.open_public")}
                  </a>
                )}
                <Button to={`/services/projets/${encodeURIComponent(found.id)}`}>
                  {t("availability.edit_publication")}
                </Button>
                {found.publication !== null && (
                  <ActionButton route="POST /api/status/components" icon="plus" to={newComponentPath(found.id)}>
                    {t("availability.add_component")}
                  </ActionButton>
                )}
              </>
            }
          />
          <p>
            <Link to={publicPagesPath}>{t("availability.back_to_pages")}</Link>
          </p>
          {components.error && <Failure error={components.error} />}
          <Loaded resource={components}>{(response) => <Components components={response.components} global={response.global} published={found.publication !== null} />}</Loaded>
        </>
      )}
    </Loaded>
  );
}

function Components({ components, global, published }: { components: StatusComponent[]; global: StatusComponent["effective"]; published: boolean }) {
  const t = useT();
  if (!published) {
    return <EmptyCard icon="eye" title={t("availability.unpublished_title")} text={t("availability.unpublished_text")} />;
  }
  if (components.length === 0) {
    return <EmptyCard icon="eye" title={t("availability.components_empty_title")} text={t("availability.components_empty_text")} />;
  }
  const allImplicit = components.every((component) => component.implicit);
  return (
    <>
      <span className="machine-state">
        <span className="field-label">{t("availability.global_label")}</span>
        {global === "" ? <Pill tone="neutral">{t("availability.global_none")}</Pill> : <StatusStatePill state={global} />}
      </span>
      {allImplicit && <p className="secondary">{t("availability.implicit_note")}</p>}
      <Card className="scroll-x">
        <Table>
          <thead>
            <tr>
              <th>{t("availability.col_component")}</th>
              <th className="wide-only">{t("availability.col_objects")}</th>
              <th className="wide-only">{t("availability.col_derived")}</th>
              <th className="th-end">{t("availability.col_shown")}</th>
            </tr>
          </thead>
          <tbody>
            {components.map((component) => (
              <ComponentRow key={component.id} component={component} />
            ))}
          </tbody>
        </Table>
      </Card>
    </>
  );
}

function ComponentRow({ component }: { component: StatusComponent }) {
  const t = useT();
  const names = component.members.map((member) => member.name || t("component.missing")).join(", ");
  const imposed = component.effective !== component.derived;
  return (
    <tr>
      <td>
        {component.implicit ? (
          <span className="cell-stack">
            <Subject icon="eye">{component.name}</Subject>
            <span className="secondary">{t("availability.implicit")}</span>
          </span>
        ) : (
          <Link className="svc" to={componentPath(component.id)}>
            <Subject icon="eye">{component.name}</Subject>
          </Link>
        )}
      </td>
      <td className="wide-only">
        <span className="cell-stack">
          <span>{t(component.members.length === 1 ? "availability.objects_count" : "availability.objects_counts", component.members.length)}</span>
          {names !== "" && <span className="secondary">{names}</span>}
        </span>
      </td>
      <td className="wide-only">
        <StatusStatePill state={component.derived} />
      </td>
      <td className="td-end">
        <span className="cell-stack end">
          <StatusStatePill state={component.effective} />
          {component.effective === "" && <span className="secondary">{t("availability.hidden")}</span>}
          {imposed && component.effective !== "" && <span className="secondary">{t("availability.imposed")}</span>}
        </span>
      </td>
    </tr>
  );
}
