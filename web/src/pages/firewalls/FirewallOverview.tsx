import { useState } from "react";

import { useFirewallInterfaces, useFirewallLeases, useFirewallRules, type FirewallDetail } from "../../api/firewalls";
import { EmptyCard } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Note } from "../../components/Card";
import { useScrollToHash } from "../../hooks/useScrollToHash";
import { useT } from "../../i18n/context";
import { readErrorLabel, ruleTotals } from "../../lib/firewalls";
import { FirewallConnection } from "./FirewallConnection";
import { FirewallGateways, FirewallGlance } from "./FirewallGlance";
import { FirewallInterfaces } from "./FirewallInterfaces";
import { FirewallLeases } from "./FirewallLeases";
import { FirewallRules } from "./FirewallRules";

// La vue d'ensemble d'un pare-feu : l'état en un coup d'œil, les interfaces en
// cartes, les règles par interface, les baux, puis la connexion repliée.
export function FirewallOverview({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const interfaces = useFirewallInterfaces(firewall.id);
  const rules = useFirewallRules(firewall.id);
  const leases = useFirewallLeases(firewall.id);
  // Les groupes de règles ouverts : une carte d'interface en ouvre un.
  const [opened, setOpened] = useState<ReadonlySet<string>>(new Set());
  const toggle = (name: string, isOpen: boolean) =>
    setOpened((current) => {
      const next = new Set(current);
      if (isOpen) {
        next.add(name);
      } else {
        next.delete(name);
      }
      return next;
    });
  const openGroup = (name: string) => {
    toggle(name, true);
    // Le groupe s'ouvre au rendu suivant : on attend qu'il le soit pour y aller.
    requestAnimationFrame(() => document.getElementById(`regles-${name}`)?.scrollIntoView({ block: "nearest", behavior: "smooth" }));
  };
  useScrollToHash(rules.data !== null);

  const neverRead = firewall.last_read_at === null;
  const ruleList = rules.data?.rules ?? [];
  return (
    <div className="firewall">
      {firewall.last_error !== "" && (
        <Note tone="warn">
          {readErrorLabel(t, firewall.last_error)} {!neverRead && t("firewall.stale")}
        </Note>
      )}
      <FirewallGlance firewall={firewall} rules={rules.data === null ? null : ruleTotals(ruleList)} />
      {neverRead ? (
        <EmptyCard icon="firewall" text={t("firewall.never_read")} />
      ) : (
        <>
          {firewall.gateways.length > 0 && <FirewallGateways gateways={firewall.gateways} />}
          <Failure error={interfaces.error} />
          {interfaces.data && interfaces.data.interfaces.length > 0 && (
            <FirewallInterfaces interfaces={interfaces.data.interfaces} rules={ruleList} onOpen={openGroup} />
          )}
          <Failure error={rules.error} />
          {rules.data && ruleList.length > 0 && <FirewallRules rules={ruleList} interfaces={interfaces.data?.interfaces ?? []} opened={opened} onToggle={toggle} />}
          {rules.data && ruleList.length === 0 && <EmptyCard icon="shield" text={t("firewall.rules_empty")} />}
          <Failure error={leases.error} />
          {leases.data && leases.data.leases.length > 0 && <FirewallLeases leases={leases.data.leases} />}
        </>
      )}
      <FirewallConnection firewall={firewall} />
    </div>
  );
}
