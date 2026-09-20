import { Link } from "react-router";

import type { NetworkResponse, Probe, Service } from "../api/types";
import { useNow } from "../hooks/useNow";
import { useCertificateThresholds, useT, type Translate } from "../i18n/context";
import { read, soonest } from "../lib/certificates";
import { internetID, peersOf, publicPorts } from "../lib/network";
import { findingText, formatBindings, needsCaution, serviceIcon } from "../lib/services";
import { Button } from "./Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "./Card";
import { Remaining } from "./Certificate";
import { Icon } from "./Icon";
import { Pill, ServicePill } from "./Pill";
import "./NetworkInspector.scss";

interface Props {
  response: NetworkResponse;
  // Les sondes de la machine : celles rattachées au service choisi disent
  // le certificat qu'il sert.
  probes: Probe[];
  selected: string | null;
  onSelect: (id: string) => void;
}

// L'inspecteur de la planche : 300 px à droite, les faits du nœud choisi ;
// sans choix, ce que la machine compte. Le domaine attend le proxy ;
// Redémarrer attend les actions.
export function NetworkInspector({ response, probes, selected, onSelect }: Props) {
  const service = response.services.find((candidate) => candidate.id === selected);
  if (selected === internetID) {
    return <InternetInspector response={response} onSelect={onSelect} />;
  }
  if (service === undefined) {
    return <MachineInspector response={response} />;
  }
  return <ServiceInspector service={service} response={response} probes={probes} onSelect={onSelect} />;
}

// Le certificat que les sondes de ce service ont vu ; le plus pressé quand
// il y en a plusieurs. Rien quand aucune sonde ne le surveille.
function CertificateRow({ service, probes }: { service: Service; probes: Probe[] }) {
  const t = useT();
  const now = useNow();
  const thresholds = useCertificateThresholds();
  const watched = probes.filter((probe) => probe.service_id === service.id).map((probe) => probe.certificate);
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
  const findings = response.services.reduce((sum, service) => sum + service.exposure.filter((finding) => finding.level === "warn").length, 0);
  return (
    <Card className="inspector">
      <CardHeader title={t("network.summary_title")} />
      <CardBody gap={12}>
        <KeyValue label={t("network.summary_services")} value={response.services.length} mono />
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
          const target = response.services.find((service) => service.id === edge.to);
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

function ServiceInspector({
  service,
  response,
  probes,
  onSelect,
}: {
  service: Service;
  response: NetworkResponse;
  probes: Probe[];
  onSelect: (id: string) => void;
}) {
  const t = useT();
  const peers = peersOf(service, response.services);
  const dependencies = response.edges.filter((edge) => edge.kind === "depends" && edge.from === service.id);
  const unresolved = service.depends_on.filter((dependency) => !dependencies.some((edge) => response.services.find((candidate) => candidate.id === edge.to)?.name === dependency.name));
  const caution = needsCaution(service);
  return (
    <Card className="inspector">
      <div className="card-h">
        <span className="svc">
          <span className="ico">
            <Icon name={serviceIcon(service)} />
          </span>
          {service.name}
        </span>
        <ServicePill service={service} />
      </div>
      <CardBody gap={12}>
        <KeyValue label={t("service.field_image")} value={service.image} mono />
        {service.group !== "" && <KeyValue label={t("service.field_group")} value={service.group} mono />}
        <KeyValue label={t("network.field_mode")} value={modeText(t, service)} mono />
        <CertificateRow service={service} probes={probes} />

        <span className="k">{t("service.field_ports")}</span>
        {service.ports.length === 0 && <span className="muted">{t("service.internal")}</span>}
        {formatBindings(service.ports).map((line) => (
          <span className="mono" key={line}>
            {line}
          </span>
        ))}

        <span className="k">{t("network.field_networks")}</span>
        {service.networks.length === 0 && <span className="muted">{t("network.none")}</span>}
        {service.networks.map((attachment) => (
          <div className="kv" key={attachment.network_id}>
            <span className="mono">{attachment.name}</span>
            <span className="mono muted">{attachment.ip !== "" ? attachment.ip : "–"}</span>
          </div>
        ))}

        <span className="k">{t("network.field_depends")}</span>
        {dependencies.length === 0 && unresolved.length === 0 && <span className="muted">{t("network.none")}</span>}
        {dependencies.map((edge) => {
          const target = response.services.find((candidate) => candidate.id === edge.to);
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
              {group.services.map((peer) => (
                <button type="button" className="link-btn mono" key={peer.id} onClick={() => onSelect(peer.id)}>
                  {peer.name}
                </button>
              ))}
            </span>
          </div>
        ))}

        <span className="k">{t("network.field_exposure")}</span>
        {service.exposure.length === 0 && (
          <Pill tone="ok" dot={false}>
            {t("network.exposure_none")}
          </Pill>
        )}
        {service.exposure.map((finding) =>
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
        <Button small icon="file" to={`/services/${service.id}`}>
          {t("network.open_service")}
        </Button>
        <Link className="secondary" to={`/services/${service.id}#journaux`}>
          {t("service.logs_title")}
        </Link>
      </div>
    </Card>
  );
}

// Le mode réseau tel qu'on le lit : host et none se traduisent, le reste
// est le nom d'un réseau ou d'un conteneur.
export function modeText(t: Translate, service: Pick<Service, "network_mode">): string {
  switch (service.network_mode) {
    case "host":
      return t("network.mode_host");
    case "none":
      return t("network.mode_none");
    case "":
      return "–";
    default:
      if (service.network_mode.startsWith("container:")) {
        return t("network.mode_container", service.network_mode.slice("container:".length, "container:".length + 12));
      }
      return service.network_mode;
  }
}
