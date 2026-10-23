import { useProbes } from "../api/probes";
import { Button } from "../components/Button";
import { Card, EmptyCard } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { ProbeTable } from "../components/ProbeTable";
import { useT } from "../i18n/context";
import { newProbePath, probeNeedsAttention, probesSubtitle } from "../lib/probes";

// La page Disponibilité : ce qu'openCloud vérifie depuis l'extérieur. Une ligne
// par sonde, son état, sa barre de disponibilité et son dernier essai.
export function Probes() {
  const t = useT();
  const probes = useProbes();
  const list = probes.data?.probes ?? [];
  const attention = list.filter(probeNeedsAttention).length;
  return (
    <>
      <PageHead
        title={t("nav.availability")}
        subtitle={probes.data ? probesSubtitle(t, list.length, attention) : ""}
        actions={
          <Button variant="primary" icon="plus" to={newProbePath()}>
            {t("probe.add")}
          </Button>
        }
      />
      {probes.data && list.length > 0 && <p className="secondary">{t("probes.intro")}</p>}
      {probes.error && <Failure error={probes.error} />}
      {probes.data && list.length === 0 && (
        <EmptyCard icon="globe" title={t("probes.empty_title")} text={t("probes.empty_text")} />
      )}
      {probes.data && list.length > 0 && (
        <Card className="scroll-x">
          <ProbeTable probes={list} days={probes.data.days} withMachine />
        </Card>
      )}
    </>
  );
}
