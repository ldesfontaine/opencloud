import type { FirewallInterface, FirewallRule } from "../../api/firewalls";
import { Card, CardHeader } from "../../components/Card";
import { Pill } from "../../components/Pill";
import { useT } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { groupRulesByInterface, interfaceCardTone, interfaceStatusLabel, interfaceTone, ruleTotals } from "../../lib/firewalls";

// Les interfaces en cartes, une par zone : son nom, son réseau, son état, et ce
// que ses règles en font. Une carte ouvre le groupe de règles de sa zone.
export function FirewallInterfaces({
  interfaces,
  rules,
  onOpen,
}: {
  interfaces: FirewallInterface[];
  rules: FirewallRule[];
  onOpen: (name: string) => void;
}) {
  const t = useT();
  const groups = groupRulesByInterface(rules, interfaces);
  return (
    <Card>
      <CardHeader title={t("firewall.interfaces_title")} aside={interfaces.length} />
      <div className="firewall-zones">
        {interfaces.map((item) => {
          const totals = ruleTotals(groups.find((group) => group.name === item.name)?.rules ?? []);
          const label = item.role === "" ? item.name : item.role;
          return (
            <button
              key={item.name}
              type="button"
              className={`firewall-zone firewall-zone-${interfaceCardTone(item.status)}`}
              onClick={() => onOpen(item.name)}
              aria-label={t("firewall.interface_open", label)}
            >
              <span className="firewall-zone-top">
                <b>{label}</b>
                <Pill tone={interfaceTone(item.status)} small>
                  {interfaceStatusLabel(t, item.status)}
                </Pill>
              </span>
              <span className="mono">{item.address === "" ? "—" : item.address}</span>
              <span className="secondary mono">{item.device === "" ? item.name : `${item.name} · ${item.device}`}</span>
              <span className="firewall-zone-counts">
                {totals.all === 0 ? (
                  <Pill tone="neutral" small dot={false}>
                    {t("firewall.interface_no_rules")}
                  </Pill>
                ) : (
                  <>
                    <Pill tone="neutral" small dot={false}>
                      {counted(t, "firewall.rules_count", totals.all)}
                    </Pill>
                    {totals.block > 0 && (
                      <Pill tone="danger" small dot={false}>
                        {counted(t, "firewall.rules_block", totals.block)}
                      </Pill>
                    )}
                  </>
                )}
              </span>
            </button>
          );
        })}
      </div>
    </Card>
  );
}
