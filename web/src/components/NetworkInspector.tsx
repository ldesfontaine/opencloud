import { Link } from "react-router";

import type { NetworkResponse, Probe, Container } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useCertificateThresholds, useT, type Translate } from "../i18n/context";
import { read, soonest } from "../lib/certificates";
import { internetID, peersOf, probesOf, publicPorts } from "../lib/network";
import { findingText, formatBindings, needsCaution, containerIcon } from "../lib/containers";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "./Card";
import { Remaining } from "./Certificate";
import { Icon } from "./Icon";
import { Pill, ContainerPill } from "./Pill";
import "./NetworkInspector.scss";

interface Props {
  response: NetworkResponse;
  // Les sondes de la machine : celles rattachées au conteneur choisi disent
  // le certificat qu'il sert.
  probes: Probe[];
  selected: string | null;
  onSelect: (id: string) => void;
}

// L'inspecteur de la planche : 300 px à droite, les faits du nœud choisi ;
// sans choix, ce que la machine compte. Le domaine attend le proxy ;
// Redémarrer attend les actions.
export function NetworkInspector({ response, probes, selected, onSelect }: Props) {
  const container = response.containers.find((candidate) => candidate.id === selected);
  if (selected === internetID) {
    return <InternetInspector response={response} onSelect={onSelect} />;
  }
  if (container === undefined) {
    return <MachineInspector response={response} />;
  }
  return <ContainerInspector container={container} response={response} probes={probes} onSelect={onSelect} />;
}

// Le certificat que les sondes de ce conteneur ont vu ; le plus pressé quand
// il y en a plusieurs. Rien quand aucune sonde ne le surveille.
function CertificateRow({ container, probes }: { container: Container; probes: Probe[] }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  const watched = probesOf(probes, container).map((probe) => probe.certificate);
  const certificate = soonest(watched);
  if (certificate === null) {
    return null;
  }
  return (
    <div className="kv">
      <span className="k">{t("cert.field_certificate")}</span>
      <Remaining reading={read(certificate, thresholds, now)} />
    </div>
  );
}

function MachineInspector({ response }: { response: NetworkResponse }) {
  const t = useT();
  const findings = response.containers.reduce((sum, container) => sum + container.exposure.filter((finding) => finding.level === "warn").length, 0);
  return (
    <Card className="inspector">
      <CardHeader title={t("network.summary_title")} />
      <CardBody gap={12}>
        <KeyValue label={t("network.summary_containers")} value={response.containers.length} mono />
        <KeyValue label={t("network.summary_groups")} value={response.groups.length} mono />
        <KeyValue label={t("network.summary_public")} value={publicPorts(response.edges).length} mono />
        <KeyValue label={t("network.summary_findings")} value={findings} mono />
        <p className="secondary">{t("network.inspector_hint")}</p>
      </CardBody>
    </Card>
  );
}

function InternetInspector({ response, onSelect }: { response: NetworkResponse; onSelect: (id: string) => void }) {
  const t = useT();
  const routes = response.edges.filter((edge) => edge.kind === "public");
  return (
    <Card className="inspector">
      <div className="card-h">
        <span className="svc">
          <span className="ico">
            <Icon name="globe" />
          </span>
          {t("network.internet")}
        </span>
      </div>
      <CardBody gap={12}>
        <span className="k">{t("network.field_routes")}</span>
        {routes.length === 0 && <span className="muted">{t("network.none")}</span>}
        {routes.map((edge) => {
          const target = response.containers.find((container) => container.id === edge.to);
          return (
            <div className="kv" key={`${edge.to}:${edge.port ?? ""}`}>
              <span className="mono">{`:${edge.port ?? ""}/${edge.protocol ?? ""}`}</span>
              <button type="button" className="link-btn" onClick={() => onSelect(edge.to)}>
                {target?.name ?? edge.to}
              </button>
            </div>
          );
        })}
      </CardBody>
    </Card>
  );
}

function ContainerInspector({
  container,
  response,
  probes,
  onSelect,
}: {
  container: Container;
  response: NetworkResponse;
  probes: Probe[];
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const peers = peersOf(container, response.containers);
  const dependencies = response.edges.filter((edge) => edge.kind === "depends" && edge.from === container.id);
  const unresolved = container.depends_on.filter((dependency) => !dependencies.some((edge) => response.containers.find((candidate) => candidate.id === edge.to)?.name === dependency.name));
  const caution = needsCaution(container);
  return (
    <Card className="inspector">
      <div className="card-h">
        <span className="svc">
          <span className="ico">
            <Icon name={containerIcon(container)} />
          </span>
          {container.name}
        </span>
        <ContainerPill container={container} />
      </div>
      <CardBody gap={12}>
        <KeyValue label={t("container.field_image")} value={container.image} mono />
        {container.group !== "" && <KeyValue label={t("container.field_group")} value={container.group} mono />}
        <KeyValue label={t("network.field_mode")} value={modeText(t, container)} mono />
        <CertificateRow container={container} probes={probes} />

        <span className="k">{t("container.field_ports")}</span>
        {container.ports.length === 0 && <span className="muted">{t("container.internal")}</span>}
        {formatBindings(container.ports).map((line) => (
          <span className="mono" key={line}>
            {line}
          </span>
        ))}

        <span className="k">{t("network.field_networks")}</span>
        {container.networks.length === 0 && <span className="muted">{t("network.none")}</span>}
        {container.networks.map((attachment) => (
          <div className="kv" key={attachment.network_id}>
            <span className="mono">{attachment.name}</span>
            <span className="mono muted">{attachment.ip !== "" ? attachment.ip : "–"}</span>
          </div>
        ))}

        <span className="k">{t("network.field_depends")}</span>
        {dependencies.length === 0 && unresolved.length === 0 && <span className="muted">{t("network.none")}</span>}
        {dependencies.map((edge) => {
          const target = response.containers.find((candidate) => candidate.id === edge.to);
          return (
            <button type="button" className="link-btn mono" key={edge.to} onClick={() => onSelect(edge.to)}>
              {target?.name ?? edge.to}
            </button>
          );
        })}
        {unresolved.map((dependency) => (
          <span className="mono muted" key={dependency.name}>
            {t("network.depends_unknown", dependency.name)}
          </span>
        ))}

        {peers.map((group) => (
          <div className="stack gap-8" key={group.network}>
            <span className="k">{t("network.field_peers", group.network)}</span>
            <span className="peers">
              {group.containers.map((peer) => (
                <button type="button" className="link-btn mono" key={peer.id} onClick={() => onSelect(peer.id)}>
                  {peer.name}
                </button>
              ))}
            </span>
          </div>
        ))}

        <span className="k">{t("network.field_exposure")}</span>
        {container.exposure.length === 0 && (
          <Pill tone="ok" dot={false}>
            {t("network.exposure_none")}
          </Pill>
        )}
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
        {caution && <span className="secondary">{t("network.exposure_hint")}</span>}
      </CardBody>
      <div className="card-h inspector-actions">
        <Button small icon="file" to={`/services/conteneurs/${container.id}`}>
          {t("network.open_container")}
        </Button>
        <Link className="secondary" to={`/services/conteneurs/${container.id}#journaux`}>
          {t("container.logs_title")}
        </Link>
      </div>
    </Card>
  );
}

// Le mode réseau tel qu'on le lit : host et none se traduisent, le reste
// est le nom d'un réseau ou d'un conteneur.
export function modeText(t: Translate, container: Pick<Container, "network_mode">): string {
  switch (container.network_mode) {
    case "host":
      return t("network.mode_host");
    case "none":
      return t("network.mode_none");
    case "":
      return "–";
    default:
      if (container.network_mode.startsWith("container:")) {
        return t("network.mode_container", container.network_mode.slice("container:".length, "container:".length + 12));
      }
      return container.network_mode;
  }
}
