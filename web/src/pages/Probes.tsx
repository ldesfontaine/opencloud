import type { ProbesResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, Empty } from "../components/Card";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { ProbeTable } from "../components/ProbeTable";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { needsAttention, probesSubtitle } from "../lib/probes";

// La page Domaines : ce qu'openCloud vérifie depuis l'extérieur. Une ligne
// par sonde, son état, sa barre de disponibilité et son dernier essai.
export function Probes() {
  const t = useT();
  const probes = useResource<ProbesResponse>("/api/probes");
  const list = probes.data?.probes ?? [];
  const attention = list.filter(needsAttention).length;
  return (
    <>
      <PageHead
        title={t("nav.domains")}
        subtitle={probes.data ? probesSubtitle(t, list.length, attention) : ""}
        actions={
          <Button variant="primary" icon="plus" to="/domaines/nouvelle">
            {t("probe.add")}
          </Button>
        }
      />
      {probes.error && <Failure error={probes.error} />}
      {probes.data && list.length === 0 && (
        <Card>
          <Empty icon="globe" title={t("probes.empty_title")} text={t("probes.empty_text")} />
        </Card>
      )}
      {probes.data && list.length > 0 && (
        <Card className="scroll-x">
          <ProbeTable probes={list} days={probes.data.days} withMachine />
        </Card>
      )}
    </>
  );
}
