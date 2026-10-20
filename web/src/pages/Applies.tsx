import { Link } from "react-router";

import { useApplies } from "../api/manifest";
import { Button } from "../components/Button";
import { Card, EmptyCard } from "../components/Card";
import { TokenBox } from "../components/Copy";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Table } from "../components/Table";
import { useT } from "../i18n/context";
import { actorText, shortHash, stateTone, summaryText } from "../lib/manifest";
import { formatClock } from "../lib/time";

// Le journal des fichiers appliqués : qui, quand, quel fichier, et comment
// ça s'est fini. Le détail d'un passage montre son plan.
export function Applies() {
  const t = useT();
  const applies = useApplies();
  const list = applies.data?.applies ?? [];
  return (
    <>
      <PageHead
        title={t("manifest.journal")}
        subtitle={applies.data ? t("manifest.journal_subtitle") : ""}
        actions={
          <Button icon="database" to="/services">
            {t("manifest.back_to_directory")}
          </Button>
        }
      />
      {applies.error && <Failure error={applies.error} />}
      {applies.data && list.length === 0 && (
        <EmptyCard icon="file" title={t("manifest.journal_empty")} text={t("manifest.journal_empty_text")}>
          <div className="card-b">
            <TokenBox value={t("manifest.journal_command")} />
          </div>
        </EmptyCard>
      )}
      {applies.data && list.length > 0 && (
        <Card className="scroll-x">
          <Table>
            <thead>
              <tr>
                <th>{t("manifest.col_when")}</th>
                <th className="wide-only">{t("manifest.col_actor")}</th>
                <th>{t("manifest.col_file")}</th>
                <th>{t("manifest.col_state")}</th>
                <th className="th-end">{t("manifest.col_changes")}</th>
              </tr>
            </thead>
            <tbody>
              {list.map((apply) => (
                <tr key={apply.id}>
                  <td className="nowrap">
                    <Link className="mono" to={`/services/annuaire/journal/${apply.id}`}>
                      {formatClock(apply.at)}
                    </Link>
                  </td>
                  <td className="secondary wide-only">{actorText(t, apply)}</td>
                  <td>
                    <span>{apply.file_name !== "" ? apply.file_name : "—"}</span> <span className="mono muted">{shortHash(apply.file_hash)}</span>
                    {apply.prune && (
                      <>
                        {" "}
                        <Pill tone="warn" dot={false}>
                          {t("manifest.prune")}
                        </Pill>
                      </>
                    )}
                  </td>
                  <td>
                    <Pill tone={stateTone(apply.state)}>{t(`manifest.state_${apply.state}`)}</Pill>
                  </td>
                  <td className="num td-end">{summaryText(apply.summary)}</td>
                </tr>
              ))}
            </tbody>
          </Table>
        </Card>
      )}
    </>
  );
}
