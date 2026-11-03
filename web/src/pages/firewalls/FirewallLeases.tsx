import { Link } from "react-router";

import type { FirewallLease } from "../../api/firewalls";
import { Card, CardHeader } from "../../components/Card";
import { Icon } from "../../components/Icon";
import { Pill } from "../../components/Pill";
import { Table } from "../../components/Table";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { formatWhen } from "../../lib/time";

// Les baux DHCP. Un bail dont la carte réseau ou l'adresse est celle d'une
// machine d'openCloud la nomme, avec un lien vers sa fiche.
export function FirewallLeases({ leases }: { leases: FirewallLease[] }) {
  const t = useT();
  const known = leases.filter((lease) => lease.machine !== null).length;
  return (
    <Card className="scroll-x">
      <CardHeader title={t("firewall.leases_title")} aside={known === 0 ? leases.length : `${leases.length} · ${counted(t, "firewall.leases_known", known)}`} />
      <Table>
        <thead>
          <tr>
            <th>{t("firewall.col_address")}</th>
            <th>{t("firewall.col_hostname")}</th>
            <th>{t("firewall.col_machine")}</th>
            <th className="wide-only">{t("firewall.col_mac")}</th>
            <th className="wide-only">{t("firewall.col_interface")}</th>
            <th>{t("firewall.col_expires")}</th>
          </tr>
        </thead>
        <tbody>
          {leases.map((lease, index) => (
            <LeaseRow key={index} lease={lease} />
          ))}
        </tbody>
      </Table>
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
      <td>
        {lease.machine === null ? (
          <span className="secondary">{t("firewall.lease_unknown_machine")}</span>
        ) : (
          <Link className="firewall-machine" to={`/machines/${lease.machine.id}`}>
            <Icon name="server" />
            {lease.machine.name}
          </Link>
        )}
      </td>
      <td className="num wide-only">{lease.mac === "" ? "—" : lease.mac}</td>
      <td className="wide-only">{lease.interface === "" ? "—" : lease.interface}</td>
      <td className="secondary nowrap">
        {lease.is_static ? <Pill tone="neutral">{t("firewall.lease_static")}</Pill> : lease.expires_at === null ? t("firewall.lease_no_expiry") : formatWhen(lease.expires_at, now)}
      </td>
    </tr>
  );
}
