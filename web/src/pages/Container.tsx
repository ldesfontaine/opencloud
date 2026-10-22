import { useEffect, useRef } from "react";
import { Link, useLocation, useParams } from "react-router";

import { useContainer, type Container, type ContainerResponse, type Transition } from "../api/containers";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, EmptyCard, KeyValue, Note } from "../components/Card";
import { DirectoryCard } from "../components/DirectoryCard";
import { Fold } from "../components/Disclosure";
import { Loaded } from "../components/Loaded";
import { LogViewer } from "../components/LogViewer";
import { PageHead } from "../components/PageHead";
import { Dot, ContainerPill } from "../components/Pill";
import { List, RowText, RowWhen } from "../components/Table";
import { UpdateCard } from "../components/UpdateCard";
import { useNow } from "../hooks/useNow";
import { useI18n } from "../i18n/context";
import { adoptPath, isOrphan } from "../lib/gaps";
import { findingText, formatBindings, containerPill, transitionPassage } from "../lib/containers";
import { modeText } from "../lib/network";
import { newProbePath } from "../lib/probes";
import { ago, formatClock, formatDuration, since } from "../lib/time";
import { formatBytes, formatCPU } from "../lib/units";
import "./Container.scss";

// La fiche d'un conteneur : la pastille, les détails, l'historique d'état, les
// journaux en direct, et de quoi le mettre sous sonde.
export function ContainerPage() {
  const { id = "" } = useParams();
  const response = useContainer(id);
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
      <EmptyCard
        icon="search"
        title={t("container.not_found_title")}
        text={t("container.not_found_text")}
        action={<Button to="/services/conteneurs">{t("services.tab_containers")}</Button>}
      />
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
            <Button icon="globe" to={newProbePath({ service: container.declared_service_id, container: container.id })}>
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
                  <span className="mono">{formatCPU(language, container.current.cpu_percent)}</span>
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
      <div className="container-facts">
        <ContainerDetails container={container} now={now} />
        <ContainerHistory transitions={response.transitions} now={now} />
      </div>
      <LogsCard containerID={container.id} />
    </>
  );
}

// Les faits de la fiche, sans ce que l'en-tête de page dit déjà (machine,
// image, durée). Une ligne vide ne s'affiche pas.
function ContainerDetails({ container, now }: { container: Container; now: number }) {
  const { t } = useI18n();
  return (
    <Card>
      <CardHeader title={t("container.details_title")} aside={<Link to={`/machines/${container.machine_id}/reseau`}>{t("network.open_graph")}</Link>} />
      <CardBody gap={10}>
        <div className="container-details">
          {container.group !== "" && <KeyValue label={t("container.field_group")} value={container.group} mono />}
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
          <KeyValue label={t("network.field_mode")} value={modeText(t, container)} mono />
          {container.ports.length > 0 && (
            <KeyValue label={t("container.field_ports")} value={formatBindings(container.ports).map((line) => <div key={line}>{line}</div>)} mono />
          )}
          {container.networks.length > 0 && (
            <KeyValue
              label={t("network.field_networks")}
              value={container.networks.map((attachment) => <div key={attachment.network_id}>{attachment.ip !== "" ? `${attachment.name} · ${attachment.ip}` : attachment.name}</div>)}
              mono
            />
          )}
          {container.depends_on.length > 0 && (
            <KeyValue label={t("network.field_depends")} value={container.depends_on.map((dependency) => dependency.name).join(", ")} mono />
          )}
        </div>
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
  );
}

// Les changements d'état les plus récents, les plus anciens repliés : l'API
// en rend cinquante au plus.
const visibleTransitions = 8;

function ContainerHistory({ transitions, now }: { transitions: Transition[]; now: number }) {
  const { t } = useI18n();
  const recent = transitions.slice(0, visibleTransitions);
  const older = transitions.slice(visibleTransitions);
  return (
    <Card>
      <CardHeader title={t("container.history_title")} />
      <p className="secondary container-history-text">{t("container.history_text")}</p>
      {transitions.length === 0 ? (
        <Empty text={t("container.transitions_empty")} />
      ) : (
        <>
          <List>
            {recent.map((transition) => (
              <TransitionRow key={transition.id} transition={transition} now={now} />
            ))}
          </List>
          {older.length > 0 && (
            <div className="container-history-more">
              <Fold label={t("container.history_more", older.length)} initiallyOpen={false}>
                <List>
                  {older.map((transition) => (
                    <TransitionRow key={transition.id} transition={transition} now={now} />
                  ))}
                </List>
              </Fold>
            </div>
          )}
        </>
      )}
    </Card>
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
            {transitionPassage(t, transition)}
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
