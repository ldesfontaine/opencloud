import { useParams } from "react-router";

import { segment } from "../api/client";
import type { Apply } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { Loaded } from "../components/Loaded";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { PlanView } from "../components/PlanView";
import { useResource } from "../hooks/useResource";
import { useI18n } from "../i18n/context";
import { actorText, failedObjectText, stateTone, summaryText, totalChanges, writtenText } from "../lib/manifest";
import { formatClock } from "../lib/time";

// Un passage du journal : ses faits, pourquoi il s'est arrêté s'il l'a été, et
// le plan que l'opérateur avait confirmé, dans l'ordre d'écriture.
export function ApplyDetail() {
  const { id = "" } = useParams();
  const apply = useResource<Apply>(() => `/api/manifest/applies/${segment(id)}`);
  return <Loaded resource={apply}>{(data) => <ApplyView apply={data} />}</Loaded>;
}

function ApplyView({ apply }: { apply: Apply }) {
  const { t, language } = useI18n();
  const changes = apply.changes ?? [];
  const total = totalChanges(apply.summary);
  return (
    <>
      <PageHead
        title={apply.file_name !== "" ? apply.file_name : apply.id}
        subtitle={formatClock(apply.at)}
        actions={
          <Button icon="file" to="/services/annuaire/journal">
            {t("manifest.journal")}
          </Button>
        }
      />
      {apply.state === "failed" && (
        <Note tone="danger">
          {t("manifest.failed_at", apply.done + 1, total, failedObjectText(t, apply.error_object))} {t(apply.error)}
        </Note>
      )}
      {apply.state === "confirmed" && <Note tone="warn">{t("manifest.running")}</Note>}
      <Card>
        <CardHeader title={t("manifest.facts")} aside={<Pill tone={stateTone(apply.state)}>{t(`manifest.state_${apply.state}`)}</Pill>} />
        <CardBody gap={8}>
          <KeyValue label={t("manifest.fact_started")} value={formatClock(apply.at)} mono />
          {apply.finished_at && <KeyValue label={t("manifest.fact_finished")} value={formatClock(apply.finished_at)} mono />}
          <KeyValue label={t("manifest.col_actor")} value={actorText(t, apply)} />
          <KeyValue label={t("manifest.fact_file_hash")} value={apply.file_hash} mono />
          <KeyValue label={t("manifest.fact_plan_hash")} value={apply.plan_hash} mono />
          <KeyValue label={t("manifest.fact_options")} value={apply.prune ? t("manifest.prune") : t("manifest.no_options")} />
          <KeyValue label={t("manifest.col_changes")} value={`${summaryText(apply.summary)} · ${writtenText(t, language, apply.done, total)}`} mono />
        </CardBody>
      </Card>
      <Card>
        <CardHeader title={t("manifest.plan_title")} />
        {changes.length > 0 ? <PlanView changes={changes} done={apply.state === "applied" ? changes.length : apply.done} /> : <Empty text={t("manifest.plan_empty")} />}
      </Card>
    </>
  );
}
