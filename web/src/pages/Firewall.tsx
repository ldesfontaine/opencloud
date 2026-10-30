import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import {
  removeFirewall,
  useFirewall,
  useFirewallInterfaces,
  useFirewallLeases,
  useFirewallRules,
  type FirewallDetail,
  type FirewallGateway,
  type FirewallInterface,
  type FirewallLease,
  type FirewallRule,
} from "../api/firewalls";
import { Button } from "../components/Button";
import { Card, CardBody, CardHeader, Empty, KeyValue, Note } from "../components/Card";
import { ConfirmInline } from "../components/ConfirmInline";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { Select } from "../components/Select";
import { Subject, Table } from "../components/Table";
import { useAction } from "../hooks/useAction";
import { useNow } from "../hooks/useNow";
import { useI18n, useT } from "../i18n/context";
import {
  directionLabel,
  endpointLabel,
  firewallStateLabel,
  firewallTone,
  gatewayDelay,
  gatewayLoss,
  gatewayStatusLabel,
  gatewayTone,
  interfaceStatusLabel,
  interfaceTone,
  readErrorLabel,
  ruleActionLabel,
  ruleActionTone,
  ruleInterfaces,
  rulesOfInterface,
} from "../lib/firewalls";
import { formatWhen } from "../lib/time";
import { NotFound } from "./NotFound";
import "./Firewall.scss";

// La fiche d'un pare-feu : sa connexion, puis ce que son dernier relevé réussi
// a montré. Lecture seule : rien n'est écrit dans le pare-feu.
export function FirewallPage() {
  const { id = "" } = useParams();
  const firewall = useFirewall(id);
  if (firewall.error?.status === 404) {
    return <NotFound />;
  }
  if (!firewall.data) {
    return firewall.error ? <Failure error={firewall.error} /> : null;
  }
  return <FirewallView firewall={firewall.data} />;
}

function FirewallView({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const navigate = useNavigate();
  const removal = useAction();
  const remove = () => {
    removal.run(() => removeFirewall(firewall.id), { topic: "firewalls", onDone: () => void navigate("/pare-feu") });
  };
  return (
    <>
      <PageHead
        title={firewall.name}
        subtitle={t(`firewall.kind_${firewall.kind}`)}
        actions={
          <>
            <Button icon="list" to="/pare-feu">
              {t("firewall.back")}
            </Button>
            <Button icon="settings" to={`/pare-feu/${firewall.id}/modifier`}>
              {t("firewall.edit")}
            </Button>
            <ConfirmInline question={t("firewall.remove_question")} onConfirm={remove} variant="danger" icon="x" disabled={removal.busy}>
              {t("firewall.remove")}
            </ConfirmInline>
          </>
        }
      />
      <Failure error={removal.error} />
      <div className="firewall">
        <ConnectionCard firewall={firewall} />
        <InterfacesCard firewall={firewall} />
        <GatewaysCard gateways={firewall.gateways} wasRead={firewall.last_read_at !== null} />
        <RulesCard firewall={firewall} />
        <LeasesCard firewall={firewall} />
      </div>
    </>
  );
}

function ConnectionCard({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const now = useNow();
  return (
    <Card>
      <CardHeader
        title={t("firewall.card_title")}
        aside={
          <Pill tone={firewallTone(firewall)} small>
            {firewallStateLabel(t, firewall)}
          </Pill>
        }
      />
      <CardBody gap={10}>
        {firewall.last_error !== "" && (
          <Note tone="warn">
            {readErrorLabel(t, firewall.last_error)} {firewall.last_read_at !== null && t("firewall.stale")}
          </Note>
        )}
        <KeyValue label={t("firewall.field_connector")} value={t(`firewall.kind_${firewall.kind}`)} />
        <KeyValue label={t("firewall.field_api")} value={firewall.api_url} mono />
        <KeyValue label={t("firewall.field_version")} value={firewall.version === "" ? "—" : firewall.version} mono />
        <KeyValue label={t("firewall.field_key")} value={firewall.api_key} mono />
        <KeyValue label={t("firewall.field_read")} value={firewall.last_read_at === null ? t("firewall.never_read") : formatWhen(firewall.last_read_at, now)} />
        <KeyValue label={t("firewall.field_certificate")} value={firewall.is_pinned ? t("firewall.pinned") : t("firewall.system_trust")} />
      </CardBody>
    </Card>
  );
}

function InterfacesCard({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const interfaces = useFirewallInterfaces(firewall.id);
  const list = interfaces.data?.interfaces ?? [];
  return (
    <Card className="scroll-x">
      <CardHeader title={t("firewall.interfaces_title")} aside={list.length} />
      <Failure error={interfaces.error} />
      {interfaces.data && list.length === 0 && <Empty text={firewall.last_read_at === null ? t("firewall.never_read") : t("firewall.interfaces_empty")} />}
      {list.length > 0 && (
        <Table>
          <thead>
            <tr>
              <th>{t("firewall.col_name")}</th>
              <th className="wide-only">{t("firewall.col_role")}</th>
              <th className="wide-only">{t("firewall.col_device")}</th>
              <th>{t("firewall.col_address")}</th>
              <th>{t("firewall.col_state")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((item) => (
              <InterfaceRow key={item.name} item={item} />
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function InterfaceRow({ item }: { item: FirewallInterface }) {
  const t = useT();
  return (
    <tr>
      <td>
        <Subject icon="network">{item.name}</Subject>
      </td>
      <td className="wide-only">{item.role}</td>
      <td className="num wide-only">{item.device}</td>
      <td className="num">{item.address === "" ? "—" : item.address}</td>
      <td>
        <Pill tone={interfaceTone(item.status)}>{interfaceStatusLabel(t, item.status)}</Pill>
      </td>
    </tr>
  );
}

function GatewaysCard({ gateways, wasRead }: { gateways: FirewallGateway[]; wasRead: boolean }) {
  const t = useT();
  const { language } = useI18n();
  return (
    <Card className="scroll-x">
      <CardHeader title={t("firewall.gateways_title")} aside={gateways.length} />
      {gateways.length === 0 ? (
        <Empty text={wasRead ? t("firewall.gateways_empty") : t("firewall.never_read")} />
      ) : (
        <Table>
          <thead>
            <tr>
              <th>{t("firewall.col_name")}</th>
              <th className="wide-only">{t("firewall.col_address")}</th>
              <th>{t("firewall.col_state")}</th>
              <th className="wide-only">{t("firewall.col_delay")}</th>
              <th className="wide-only">{t("firewall.col_loss")}</th>
            </tr>
          </thead>
          <tbody>
            {gateways.map((gateway) => (
              <tr key={gateway.name}>
                <td>
                  <Subject icon="globe">{gateway.name}</Subject>
                </td>
                <td className="num wide-only">{gateway.address}</td>
                <td>
                  <Pill tone={gatewayTone(gateway.status)}>{gatewayStatusLabel(t, gateway.status)}</Pill>
                </td>
                <td className="num wide-only">{gatewayDelay(gateway, language)}</td>
                <td className="num wide-only">{gatewayLoss(gateway, language)}</td>
              </tr>
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function RulesCard({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const rules = useFirewallRules(firewall.id);
  const [interfaceName, setInterfaceName] = useState("");
  const all = rules.data?.rules ?? [];
  const shown = rulesOfInterface(all, interfaceName);
  const options = [{ value: "", label: t("firewall.rules_all_interfaces") }, ...ruleInterfaces(all).map((name) => ({ value: name, label: name }))];
  return (
    <Card className="scroll-x">
      <CardHeader title={t("firewall.rules_title")} aside={shown.length} />
      <Failure error={rules.error} />
      {rules.data && all.length === 0 && <Empty text={firewall.last_read_at === null ? t("firewall.never_read") : t("firewall.rules_empty")} />}
      {all.length > 0 && (
        <>
          <div className="firewall-filter">
            <p className="secondary">{t("firewall.rules_note")}</p>
            <Select value={interfaceName} options={options} onChange={setInterfaceName} aria-label={t("firewall.rules_filter")} />
          </div>
          <Table>
            <thead>
              <tr>
                <th>{t("firewall.col_action")}</th>
                <th>{t("firewall.col_interface")}</th>
                <th className="wide-only">{t("firewall.col_direction")}</th>
                <th className="wide-only">{t("firewall.col_protocol")}</th>
                <th>{t("firewall.col_source")}</th>
                <th>{t("firewall.col_destination")}</th>
                <th className="wide-only">{t("firewall.col_description")}</th>
              </tr>
            </thead>
            <tbody>
              {shown.map((rule, index) => (
                <RuleRow key={index} rule={rule} />
              ))}
            </tbody>
          </Table>
        </>
      )}
    </Card>
  );
}

function RuleRow({ rule }: { rule: FirewallRule }) {
  const t = useT();
  return (
    <tr className={rule.is_enabled ? undefined : "firewall-off"}>
      <td>
        <Pill tone={ruleActionTone(rule)}>{ruleActionLabel(t, rule.action)}</Pill>
        {!rule.is_enabled && <span className="secondary firewall-disabled">{t("firewall.rule_disabled")}</span>}
      </td>
      <td className="num">{rule.interface}</td>
      <td className="wide-only">{directionLabel(t, rule.direction)}</td>
      <td className="num wide-only">{rule.protocol}</td>
      <td className="num">{endpointLabel(rule.source, rule.source_port)}</td>
      <td className="num">{endpointLabel(rule.destination, rule.destination_port)}</td>
      <td className="secondary wide-only">{rule.description}</td>
    </tr>
  );
}

function LeasesCard({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const leases = useFirewallLeases(firewall.id);
  const list = leases.data?.leases ?? [];
  return (
    <Card className="scroll-x">
      <CardHeader title={t("firewall.leases_title")} aside={list.length} />
      <Failure error={leases.error} />
      {leases.data && list.length === 0 && <Empty text={firewall.last_read_at === null ? t("firewall.never_read") : t("firewall.leases_empty")} />}
      {list.length > 0 && (
        <Table>
          <thead>
            <tr>
              <th>{t("firewall.col_address")}</th>
              <th>{t("firewall.col_hostname")}</th>
              <th className="wide-only">{t("firewall.col_mac")}</th>
              <th className="wide-only">{t("firewall.col_interface")}</th>
              <th className="wide-only">{t("firewall.col_server")}</th>
              <th>{t("firewall.col_expires")}</th>
            </tr>
          </thead>
          <tbody>
            {list.map((lease, index) => (
              <LeaseRow key={index} lease={lease} />
            ))}
          </tbody>
        </Table>
      )}
    </Card>
  );
}

function LeaseRow({ lease }: { lease: FirewallLease }) {
  const t = useT();
  const now = useNow();
  return (
    <tr>
      <td className="num">{lease.address}</td>
      <td>{lease.hostname === "" ? "—" : lease.hostname}</td>
      <td className="num wide-only">{lease.mac === "" ? "—" : lease.mac}</td>
      <td className="wide-only">{lease.interface === "" ? "—" : lease.interface}</td>
      <td className="wide-only">{lease.server}</td>
      <td className="secondary nowrap">
        {lease.is_static ? <Pill tone="neutral">{t("firewall.lease_static")}</Pill> : lease.expires_at === null ? t("firewall.lease_no_expiry") : formatWhen(lease.expires_at, now)}
      </td>
    </tr>
  );
}
