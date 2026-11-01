import type { ReactNode } from "react";
import { Link } from "react-router";

import type { UndeclaredGroup } from "../../api/directory";
import { proposedExportPath } from "../../api/manifest";
import { Button } from "../../components/Button";
import { Card, CardHeader } from "../../components/Card";
import { Fold } from "../../components/Disclosure";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { Subject, Table } from "../../components/Table";
import { useProposalActions } from "../../hooks/useProposalActions";
import { useT } from "../../i18n/context";
import { acceptanceTarget, containerCount, editBeforeAcceptPath, hasProposals, ignoredOf, ignoreKeyOf, orphanKey, proposalsOf } from "../../lib/gaps";
import "../../components/ServiceTable.scss";

// Ce que les machines font tourner sans que l'annuaire le déclare : une
// proposition par service Compose (traefik, authelia et crowdsec d'un même
// projet sont trois propositions) ou par conteneur. L'opérateur l'accepte
// (le service est déclaré, d'un clic), la modifie avant de l'accepter, ou la
// dit voulue en l'ignorant. Les ignorés se replient dessous, où l'on peut les
// proposer de nouveau. Un conteneur dont le label ne vise rien n'a pas de
// proposition : sa pastille dit le motif, on le corrige par le label. Le
// fichier de l'annuaire peut aussi se proposer d'un coup, à relire avant de
// l'appliquer. Sans proposition ni ignoré, la section n'est pas là.
export function Proposals({ groups }: { groups: UndeclaredGroup[] }) {
  const t = useT();
  const actions = useProposalActions();
  const proposals = proposalsOf(groups);
  const ignored = ignoredOf(groups);
  if (proposals.length === 0 && ignored.length === 0) {
    return null;
  }
  return (
    <Card className="scroll-x">
      <CardHeader
        title={t("gap.proposals_title")}
        aside={
          <span className="cluster">
            {hasProposals(groups) && (
              <Button small variant="ghost" icon="file" href={proposedExportPath} download="">
                {t("manifest.export_propose")}
              </Button>
            )}
            <span className="num">{containerCount(proposals)}</span>
          </span>
        }
      />
      <Failure error={actions.error} />
      {proposals.length > 0 && (
        <>
          <p className="secondary card-b">{t("gap.proposals_text")}</p>
          <Table>
            <tbody>
              {proposals.map((group) => (
                <GroupRow key={groupKey(group)} group={group}>
                  {group.adoptable && (
                    <>
                      <Button small variant="primary" disabled={actions.busy} onClick={() => actions.accept(acceptanceTarget(group))}>
                        {t("gap.accept")}
                      </Button>
                      <Button small variant="ghost" to={editBeforeAcceptPath(acceptanceTarget(group))}>
                        {t("gap.edit_before_accept")}
                      </Button>
                    </>
                  )}
                  <Button small variant="ghost" disabled={actions.busy} onClick={() => actions.ignore(ignoreKeyOf(group))}>
                    {t("gap.ignore")}
                  </Button>
                </GroupRow>
              ))}
            </tbody>
          </Table>
        </>
      )}
      {ignored.length > 0 && (
        <div className="card-b">
          <Fold label={t("gap.ignored_fold", ignored.length)} initiallyOpen={false}>
            <p className="secondary">{t("gap.ignored_text")}</p>
            <Table>
              <tbody>
                {ignored.map((group) => (
                  <GroupRow key={groupKey(group)} group={group}>
                    <Button small variant="ghost" disabled={actions.busy} onClick={() => actions.stopIgnoring(ignoreKeyOf(group))}>
                      {t("gap.unignore")}
                    </Button>
                  </GroupRow>
                ))}
              </tbody>
            </Table>
          </Fold>
        </div>
      )}
    </Card>
  );
}

function groupKey(group: UndeclaredGroup): string {
  return `${group.machine_id} ${group.key_kind} ${group.key_value}`;
}

// Une ligne : la clé et sa sorte, la machine, les conteneurs du groupe, puis
// les gestes que l'appelant choisit.
function GroupRow({ group, children }: { group: UndeclaredGroup; children: ReactNode }) {
  const t = useT();
  return (
    <tr>
      <td>
        <Subject icon="box">
          <span className="svc-name">
            <span className="mono">{group.compose_service === "" ? group.key_value : group.compose_service}</span>
            <span className="mono muted">{group.project === "" ? t(`gap.key_${group.key_kind}`) : t("services.group_project", group.project)}</span>
          </span>
        </Subject>
      </td>
      <td className="wide-only">
        <Link to={`/machines/${group.machine_id}`}>{group.machine_name}</Link>
      </td>
      <td>
        <span className="stack">
          {group.containers.map((container) => (
            <span key={container.id} className="cluster">
              <Link className="mono" to={`/services/conteneurs/${container.id}`}>
                {container.name}
              </Link>
              {container.label_problem !== "" && (
                <span title={t(orphanKey(container.label_problem))}>
                  <Pill tone="warn" dot={false}>
                    {t("gap.orphan")}
                  </Pill>
                </span>
              )}
            </span>
          ))}
        </span>
      </td>
      <td className="td-end">
        <span className="cluster proposal-actions">{children}</span>
      </td>
    </tr>
  );
}
