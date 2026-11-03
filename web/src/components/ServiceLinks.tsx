import { Link } from "react-router";

import { confirmLink, deleteLink, dismissLink, restoreLink, useServiceLinks, type LinkEnd, type ServiceLink } from "../api/links";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useT, type Translate } from "../i18n/context";
import { endName, freshnessText, isSource, proofTone, sourceText } from "../lib/links";
import { Button } from "./Button";
import { Card, CardHeader, Empty } from "./Card";
import { ConfirmInline } from "./ConfirmInline";
import { Fold } from "./Disclosure";
import { Failure } from "./Failure";
import { Loaded } from "./Loaded";
import { MissingRight } from "./MissingRight";
import { Pill } from "./Pill";
import { Table } from "./Table";

// Les liens d'un service : ce qu'il utilise et ce qui l'utilise. Chaque ligne
// dit sa preuve (déclaré, déduit, observé) et sa fraîcheur ; un lien tiré de
// la configuration d'un outil ne se retire pas d'ici, il se retire de sa
// source. Une route déduite se confirme (elle devient la vôtre) ou s'écarte
// (elle ne revient plus, et se rétablit depuis le pli « Écartés »). Un lien lu
// dans l'environnement des conteneurs est écrit à chaque inventaire de
// l'agent : sans le droit observe.env sur la machine, la carte le dit et
// porte le geste qui l'ajoute. Une connexion vue sur un port du service (table
// de l'hôte, sans droit élevé) a pour source un compte, une machine du réseau
// ou Internet : ce n'est pas une instance, elle n'a ni fiche ni geste. L'ajout ouvre une fenêtre à l'adresse du
// service.
export function ServiceLinks({ serviceID }: { serviceID: string }) {
  const t = useT();
  const links = useServiceLinks(serviceID);
  const add = (
    <Button small variant="ghost" icon="plus" to={`/services/annuaire/${serviceID}/liens/nouveau`}>
      {t("link.add")}
    </Button>
  );
  return (
    <Card className="scroll-x">
      <CardHeader title={t("link.title")} aside={add} />
      <Loaded resource={links}>
        {(data) => {
          const active = data.links.filter((link) => !link.dismissed);
          const dismissed = data.links.filter((link) => link.dismissed);
          return (
            <>
              {data.environment?.right === "missing" && (
                <div className="card-b">
                  <MissingRight machineID={data.environment.machine_id} right="observe.env">
                    {t("link.environment_off")}
                  </MissingRight>
                </div>
              )}
              {active.length === 0 ? <Empty text={t("link.empty")} /> : <LinkTable links={active} />}
              {dismissed.length > 0 && (
                <div className="card-b">
                  <Fold label={t("link.dismissed_title", dismissed.length)} initiallyOpen={false}>
                    <p className="secondary">{t("link.dismissed_text")}</p>
                    <LinkTable links={dismissed} />
                  </Fold>
                </div>
              )}
            </>
          );
        }}
      </Loaded>
    </Card>
  );
}

function LinkTable({ links }: { links: ServiceLink[] }) {
  const t = useT();
  return (
    <Table>
      <thead>
        <tr>
          <th>{t("link.col_direction")}</th>
          <th>{t("link.col_peer")}</th>
          <th>{t("link.col_proof")}</th>
          <th>{t("link.col_fresh")}</th>
          <th className="th-end" />
        </tr>
      </thead>
      <tbody>
        {links.map((link) => (
          <LinkRow key={link.id} link={link} />
        ))}
      </tbody>
    </Table>
  );
}

function LinkRow({ link }: { link: ServiceLink }) {
  const t = useT();
  const now = useNow(60_000);
  return (
    <tr>
      <td>
        <span className="stack">
          <span>{t(`link.${link.direction}`)}</span>
          <span className="secondary">{t(`link.nature_${link.nature}`)}</span>
        </span>
      </td>
      <td>
        <PeerCell link={link} />
      </td>
      <td>
        <span className="stack">
          <span className="cluster" title={proofHelp(link, t)}>
            <Pill tone={proofTone(link.proof)}>{t(`link.proof_${link.proof}`)}</Pill>
          </span>
          <span className="secondary">{t(`link.signal_${link.signal}`)}</span>
        </span>
      </td>
      <td>
        <span className="cluster">
          <span className="secondary">{freshnessText(t, link, now)}</span>
          {link.stale && (
            <Pill tone="warn" dot={false}>
              {t("link.stale")}
            </Pill>
          )}
        </span>
      </td>
      <td className="td-end">
        <LinkActions link={link} />
      </td>
    </tr>
  );
}

// L'autre bout : le service déclaré qui porte l'instance, sinon le conteneur
// vu, sinon la clé nue avec « Non déclaré ». La machine se dit quand ce n'est
// pas celle du service.
function PeerCell({ link }: { link: ServiceLink }) {
  const t = useT();
  const peer = link.peer;
  const otherMachine = peer.machine_id !== link.self.machine_id;
  return (
    <span className="stack">
      <span className="cluster">
        <PeerName peer={peer} />
        {peer.service_id === "" && !isSource(peer) && (
          <span title={t("link.undeclared_help")}>
            <Pill tone="neutral" dot={false}>
              {t("link.undeclared")}
            </Pill>
          </span>
        )}
      </span>
      <span className="secondary">
        {[otherMachine ? peer.machine_name : "", link.port > 0 ? t("link.port", String(link.port)) : ""].filter((part) => part !== "").join(" · ")}
      </span>
    </span>
  );
}

function PeerName({ peer }: { peer: LinkEnd }) {
  const t = useT();
  if (isSource(peer)) {
    return <span>{sourceText(t, peer)}</span>;
  }
  if (peer.service_id !== "") {
    return <Link to={`/services/annuaire/${peer.service_id}`}>{peer.service_name}</Link>;
  }
  if (peer.container_id !== "") {
    return (
      <Link className="mono" to={`/services/conteneurs/${peer.container_id}`}>
        {peer.label}
      </Link>
    );
  }
  return <span className="mono">{peer.label}</span>;
}

// Ce que l'opérateur peut faire de ce lien : décider d'une route proposée, la
// rétablir si elle est écartée, retirer ce qu'il a écrit ; un lien d'outil ou
// d'environnement n'a pas de geste ici, seulement où le changer.
function LinkActions({ link }: { link: ServiceLink }) {
  const t = useT();
  if (link.proposed) {
    return <DecideLink link={link} />;
  }
  if (link.dismissed) {
    return <RestoreLink link={link} />;
  }
  return link.removable ? (
    <RemoveLink link={link} />
  ) : (
    <span className="muted" title={t(fromHelpKey(link))}>
      —
    </span>
  );
}

// L'aide d'une preuve qui s'affirme sans être sûre : la route proposée, la
// connexion vue.
function proofHelp(link: ServiceLink, t: Translate): string | undefined {
  if (link.proposed) {
    return t("link.deduced_help");
  }
  return link.proof === "observed" ? t("link.observed_help") : undefined;
}

// Où changer un lien que cet écran ne retire pas.
function fromHelpKey(link: ServiceLink): string {
  switch (link.signal) {
    case "environment":
      return "link.from_environment_help";
    case "host_connection":
      return "link.from_host_help";
    default:
      return "link.from_tool_help";
  }
}

function DecideLink({ link }: { link: ServiceLink }) {
  const t = useT();
  const action = useAction();
  return (
    <span className="stack">
      <span className="cluster">
        <Button small variant="secondary" icon="check" disabled={action.busy} onClick={() => action.run(() => confirmLink(link.id), { topic: "directory" })}>
          {t("link.confirm")}
        </Button>
        <Button small variant="ghost" icon="x" disabled={action.busy} onClick={() => action.run(() => dismissLink(link.id), { topic: "directory" })}>
          {t("link.dismiss")}
        </Button>
      </span>
      <Failure error={action.error} />
    </span>
  );
}

function RestoreLink({ link }: { link: ServiceLink }) {
  const t = useT();
  const action = useAction();
  return (
    <span className="stack">
      <Button small variant="ghost" disabled={action.busy} onClick={() => action.run(() => restoreLink(link.id), { topic: "directory" })}>
        {t("link.restore")}
      </Button>
      <Failure error={action.error} />
    </span>
  );
}

function RemoveLink({ link }: { link: ServiceLink }) {
  const t = useT();
  const action = useAction();
  return (
    <span className="stack">
      <ConfirmInline small icon="x" question={t("link.remove_confirm", endName(link.peer))} onConfirm={() => action.run(() => deleteLink(link.id), { topic: "directory" })} disabled={action.busy}>
        {t("link.remove")}
      </ConfirmInline>
      <Failure error={action.error} />
    </span>
  );
}
