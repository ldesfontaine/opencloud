import { useProbes } from "../../api/probes";
import { Card, EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { ProbeTable } from "../../components/ProbeTable";
import { useT } from "../../i18n/context";
import { probeNeedsAttention, probesSubtitle } from "../../lib/probes";

// Les sondes : une ligne par sonde, son état, sa barre de disponibilité et son
// dernier essai.
export function ProbesTab() {
  const t = useT();
  const probes = useProbes();
  const list = probes.data?.probes ?? [];
  const attention = list.filter(probeNeedsAttention).length;
  return (
    <>
      {probes.data && list.length > 0 && <p className="secondary">{probesSubtitle(t, list.length, attention)}</p>}
      {probes.data && list.length > 0 && <p className="secondary">{t("probes.intro")}</p>}
      {probes.error && <Failure error={probes.error} />}
      {probes.data && list.length === 0 && <EmptyCard icon="globe" title={t("probes.empty_title")} text={t("probes.empty_text")} />}
      {probes.data && list.length > 0 && (
        <Card className="scroll-x">
          <ProbeTable probes={list} days={probes.data.days} withMachine />
        </Card>
      )}
    </>
  );
}
