import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { api } from "../api/client";
import type { Current, Machine, MachinesResponse, TokenResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue } from "../components/Card";
import { Failure } from "../components/Failure";
import { MachineHead, machineTabs, Tabs } from "../components/MachineHead";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { ResourceHistory, ResourceStats } from "../components/Resources";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";
import { seenAgo } from "../lib/time";
import { NotFound } from "./NotFound";

// La page d'une machine : l'en-tête de la direction artistique, les
// onglets, le résumé avec ses quatre chiffres clés et l'historique ; les
// autres onglets attendent leur fonctionnalité.
export function MachinePage() {
  const { id = "", tab = "" } = useParams();
  const machines = useResource<MachinesResponse>("/api/machines");
  const known = machineTabs.some((candidate) => candidate.slug === tab);
  const current = machines.data?.machines.find((machine) => machine.id === id);
  if (!known || (machines.data && !current)) {
    return <NotFound />;
  }
  if (!machines.data || !current) {
    return machines.error ? <Failure error={machines.error} /> : null;
  }
  return <MachineView current={current} all={machines.data.machines} tab={tab} />;
}

function MachineView({ current, all, tab }: { current: Machine; all: Machine[]; tab: string }) {
  const t = useT();
  const now = useNow();
  const resources = useResource<Current>(tab === "" ? `/api/machines/${current.id}/resources` : null);
  return (
    <>
      <PageHead title={current.name} actions={!current.local && <Actions machine={current} />} />
      <Card>
        <CardBody gap={16}>
          <MachineHead current={current} all={all} />
          <Tabs machineID={current.id} />
        </CardBody>
      </Card>
      {tab === "" ? (
        <>
          <ResourceStats current={resources.data} />
          <ResourceHistory machineID={current.id} />
          <Card>
            <CardHeader title={t("tab.summary")} />
            <CardBody gap={10}>
              <KeyValue label={t("machine.field_kind")} value={t(current.local ? "machine.kind_local" : "machine.kind_remote")} />
              <KeyValue label={t("machine.field_address")} value={current.address} mono />
              <KeyValue label={t("machine.field_os")} value={current.os} />
              <KeyValue label={t("machine.field_agent")} value={current.agent_version} mono />
              <KeyValue label={t("machine.field_seen")} value={seenAgo(t, current.last_seen_at, now)} />
            </CardBody>
          </Card>
        </>
      ) : (
        <Card>
          <Empty
            text={t("machine.tab_soon")}
            action={
              <Pill tone="neutral" dot={false}>
                {t("soon.badge")}
              </Pill>
            }
          />
        </Card>
      )}
    </>
  );
}

// Ré-enrôler donne un nouveau jeton pour le même id ; retirer coupe l'agent
// et efface l'historique, après confirmation.
function Actions({ machine }: { machine: Machine }) {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [error, setError] = useState<unknown>(null);

  const reenroll = () => {
    api
      .post<TokenResponse>(`/api/machines/${machine.id}/actions/reenroll`)
      .then((token) => {
        refresh();
        void navigate("/machines/jeton", { state: token });
      })
      .catch(setError);
  };
  const remove = () => {
    if (!window.confirm(t("machine.remove_confirm", machine.name))) {
      return;
    }
    api
      .delete(`/api/machines/${machine.id}`)
      .then(() => {
        refresh();
        void navigate("/machines");
      })
      .catch(setError);
  };

  return (
    <>
      {error !== null && <Failure error={error} />}
      <Button icon="refresh" onClick={reenroll}>
        {t("machine.reenroll")}
      </Button>
      <Button variant="danger" icon="x" onClick={remove}>
        {t("machine.remove")}
      </Button>
    </>
  );
}
