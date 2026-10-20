import { useEffect, useRef } from "react";
import { Link, useLocation, useParams } from "react-router";

import { segment } from "../api/client";
import type { ContainerResponse, Transition } from "../api/types";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { DirectoryCard } from "../components/DirectoryCard";
import { Loaded } from "../components/Loaded";
import { LogViewer } from "../components/LogViewer";
import { PageHead } from "../components/PageHead";
import { Dot, ContainerPill } from "../components/Pill";
import { List, RowText, RowWhen } from "../components/Table";
import { UpdateCard } from "../components/UpdateCard";
import { useNow } from "../hooks/useNow";
import { useResource } from "../hooks/useResource";
import { useI18n } from "../i18n/context";
import { adoptPath, isOrphan } from "../lib/gaps";
import { findingText, formatBindings, containerPill, transitionTitle } from "../lib/containers";
import { modeText } from "../lib/network";
import { ago, formatClock, formatDuration, since } from "../lib/time";
import { formatBytes, formatDecimal } from "../lib/units";

// La fiche d'un conteneur : la pastille et les faits, les transitions, les
// journaux en direct, et de quoi le mettre sous sonde.
export function ContainerPage() {
  const { id = "" } = useParams();
  const response = useResource<ContainerResponse>(() => `/api/containers/${segment(id)}`);
  return (
    <Loaded resource={response} notFound={<ContainerNotFound />}>
      {(data) => <ContainerView response={data} reload={response.reload} />}
    </Loaded>
  );
}

// Une fiche absente : jamais vue, ou détruite depuis plus de trente jours.
function ContainerNotFound() {
  const { t } = useI18n();
  return (
    <>
      <PageHead title={t("container.not_found_title")} subtitle={t("container.not_found_text")} />
      <Card>
        <Empty icon="search" title={t("container.not_found_title")} text={t("container.not_found_text")} action={<Button to="/services/conteneurs">{t("services.tab_containers")}</Button>} />
      </Card>
    </>
  );
}

function ContainerView({ response, reload }: { response: ContainerResponse; reload: () => void }) {
  const { t, language } = useI18n();
  const now = useNow();
  const container = response.container;
  return (
    <>
      <PageHead
        title={container.name}
        subtitle={container.group !== "" && container.group !== container.name ? container.group : ""}
        actions={
          // Une sonde vise le service déclaré : sans lui, il n'y a rien à
          // surveiller, et le chemin est d'abord d'adopter le conteneur.
          container.declared_service_id !== "" ? (
            <Button icon="globe" to={`/domaines/nouvelle?service=${container.declared_service_id}&container=${container.id}`}>
              {t("probe.watch_service")}
            </Button>
          ) : (
            container.archived_at === null &&
            !isOrphan(response.directory) && (
              <Button icon="plus" to={adoptPath(container.id)}>
                {t("gap.adopt")}
              </Button>
            )
          )
        }
      />
      <Card>
        <CardBody gap={16}>
          <span className="machine-state">
            <ContainerPill container={container} />
            <span className="meta">
              <Link to={`/machines/${container.machine_id}`}>{container.machine_name}</Link>
              <span className="sep" />
              <span className="mono">{container.image}</span>
              {container.state === "running" && container.started_at !== null && (
                <>
                  <span className="sep" />
                  <span>{t("container.uptime", formatDuration(t, since(container.started_at, now)))}</span>
                </>
              )}
              {container.current && (
                <>
                  <span className="sep" />
                  <span className="mono">{formatDecimal(language, container.current.cpu_percent, 0)} %</span>
                  <span className="sep" />
                  <span className="mono">
                    {formatBytes(t, language, container.current.mem_used)} / {formatBytes(t, language, container.current.mem_limit)}
                  </span>
                </>
              )}
            </span>
          </span>
        </CardBody>
      </Card>
      <UpdateCard container={container} directory={response.directory} reload={reload} />
      <DirectoryCard container={container} directory={response.directory} />
      <div className="grid-3">
        <Card>
          <CardHeader title={t("tab.summary")} />
          <CardBody gap={10}>
            <KeyValue label={t("container.field_machine")} value={<Link to={`/machines/${container.machine_id}`}>{container.machine_name}</Link>} />
            {container.group !== "" && <KeyValue label={t("container.field_group")} value={container.group} mono />}
            <KeyValue label={t("container.field_image")} value={container.image} mono />
            {container.image_id !== "" && <KeyValue label={t("container.field_image_id")} value={container.image_id.replace("sha256:", "").slice(0, 12)} mono />}
            <KeyValue label={t("container.field_container")} value={container.container_id.slice(0, 12)} mono />
            <KeyValue label={t("container.field_restarts")} value={container.restart_count} mono />
            <KeyValue label={t("container.field_created")} value={formatClock(container.created_at)} mono />
            {container.started_at !== null && <KeyValue label={t("container.field_started")} value={formatClock(container.started_at)} mono />}
            {container.finished_at !== null && container.state !== "running" && (
              <KeyValue
                label={t("container.field_finished")}
                value={`${formatClock(container.finished_at)} · ${t("container.exit_code", container.exit_code)}`}
                mono
              />
            )}
            <KeyValue label={t("container.field_seen")} value={ago(t, container.last_seen_at, now)} />
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("tab.network")} aside={<Link to={`/machines/${container.machine_id}/reseau`}>{t("network.open_graph")}</Link>} />
          <CardBody gap={10}>
            <KeyValue label={t("network.field_mode")} value={modeText(t, container)} mono />
            <KeyValue
              label={t("container.field_ports")}
              value={container.ports.length > 0 ? formatBindings(container.ports).map((line) => <div key={line}>{line}</div>) : t("container.internal")}
              mono
            />
            <KeyValue
              label={t("network.field_networks")}
              value={container.networks.length > 0 ? container.networks.map((attachment) => <div key={attachment.network_id}>{attachment.ip !== "" ? `${attachment.name} · ${attachment.ip}` : attachment.name}</div>) : t("network.none")}
              mono
            />
            <KeyValue
              label={t("network.field_depends")}
              value={container.depends_on.length > 0 ? container.depends_on.map((dependency) => dependency.name).join(", ") : t("network.none")}
              mono
            />
            {container.exposure.map((finding) =>
              finding.level === "warn" ? (
                <Note tone="warn" key={finding.kind + String(finding.port)}>
                  {findingText(t, finding)}
                </Note>
              ) : (
                <span className="secondary" key={finding.kind + String(finding.port)}>
                  {findingText(t, finding)}
                </span>
              ),
            )}
          </CardBody>
        </Card>
        <Card>
          <CardHeader title={t("container.transitions_title")} />
          {response.transitions.length > 0 ? (
            <List>
              {response.transitions.map((transition) => (
                <TransitionRow key={transition.id} transition={transition} now={now} />
              ))}
            </List>
          ) : (
            <Empty text={t("container.transitions_empty")} />
          )}
        </Card>
      </div>
      <LogsCard containerID={container.id} />
    </>
  );
}

// La carte des journaux, ancre `#journaux` : un lien qui l'annonce amène
// la carte à l'écran dès que la fiche est affichée.
function LogsCard({ containerID }: { containerID: string }) {
  const { t } = useI18n();
  const { hash } = useLocation();
  const card = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (hash === "#journaux") {
      card.current?.scrollIntoView({ block: "start" });
    }
  }, [hash]);
  return (
    <div id="journaux" ref={card}>
      <Card>
        <CardHeader title={t("container.logs_title")} />
        <LogViewer containerID={containerID} />
      </Card>
    </div>
  );
}

function TransitionRow({ transition, now }: { transition: Transition; now: number }) {
  const t = useI18n().t;
  const tone = transition.new_state === "" ? "neutral" : containerPill({ state: transition.new_state, exit_code: transition.exit_code ?? 0, health: transition.new_health, oom_killed: transition.oom_killed, killed: transition.killed }).tone;
  return (
    <div className="row">
      <Dot tone={tone} />
      <RowText
        title={
          <>
            {transitionTitle(t, transition)}
            {transition.exit_code !== null && (
              <>
                {" · "}
                <span className="mono">{t("container.exit_code", transition.exit_code)}</span>
              </>
            )}
            {transition.replayed && (
              <>
                {" · "}
                <span className="muted">{t("container.transition_replayed")}</span>
              </>
            )}
          </>
        }
      >
        {formatClock(transition.at)}
        {transition.snippet !== "" && (
          <>
            <br />
            <span className="mono">{transition.snippet.split("\n").slice(-3).join(" ⏎ ")}</span>
          </>
        )}
      </RowText>
      <RowWhen>{ago(t, transition.at, now)}</RowWhen>
    </div>
  );
}
