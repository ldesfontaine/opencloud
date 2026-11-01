import type { FirewallDetail, FirewallGateway } from "../../api/firewalls";
import { Card, CardHeader, Stat } from "../../components/Card";
import { Pill } from "../../components/Pill";
import { Dot } from "../../components/Pill";
import { useNow } from "../../hooks/useNow";
import { useI18n, useT } from "../../i18n/context";
import { counted } from "../../lib/counted";
import { firewallStateLabel, firewallTone, gatewayDelay, gatewayLoss, gatewayStatusLabel, gatewayTone, gatewayTotals, shortAddress, type RuleTotals } from "../../lib/firewalls";
import { formatWhen } from "../../lib/time";

// L'état en un coup d'œil : la version, le dernier relevé, les passerelles qui
// tiennent, les règles qui passent ou qui bloquent. Chaque tuile mène au bloc
// qu'elle chiffre.
export function FirewallGlance({ firewall, rules }: { firewall: FirewallDetail; rules: RuleTotals | null }) {
  const t = useT();
  const now = useNow();
  const gateways = gatewayTotals(firewall.gateways);
  const active = rules === null ? null : rules.all - rules.off;
  return (
    <div className="firewall-stats">
      <Stat
        icon="firewall"
        label={t("firewall.glance_version")}
        value={<span className="firewall-version">{firewall.version === "" ? "—" : firewall.version}</span>}
        foot={t("firewall.glance_version_foot", shortAddress(firewall.api_url))}
        to="#connexion"
      />
      <Stat
        icon="clock"
        label={t("firewall.glance_read")}
        value={<span className="firewall-read">{firewall.last_read_at === null ? "—" : formatWhen(firewall.last_read_at, now)}</span>}
        foot={
          <>
            <Pill tone={firewallTone(firewall)} small>
              {firewallStateLabel(t, firewall)}
            </Pill>{" "}
            {t("firewall.glance_read_foot")}
          </>
        }
        to="#connexion"
      />
      <Stat
        icon="globe"
        label={t("firewall.glance_gateways")}
        value={gateways.online}
        unit={t("firewall.glance_gateways_of", gateways.all)}
        foot={
          gateways.all === 0 ? (
            "—"
          ) : (
            <>
              {gateways.offline > 0 && (
                <Pill tone="danger" small>
                  {counted(t, "firewall.gateways_cut", gateways.offline)}
                </Pill>
              )}{" "}
              {gateways.other > 0 && (
                <Pill tone="warn" small>
                  {counted(t, "firewall.gateways_other", gateways.other)}
                </Pill>
              )}{" "}
              {gateways.offline === 0 && gateways.other === 0 && (
                <Pill tone="ok" small>
                  {t("firewall.gateways_online", gateways.online)}
                </Pill>
              )}
            </>
          )
        }
        to="#passerelles"
      />
      <Stat
        icon="shield"
        label={t("firewall.glance_rules")}
        value={active ?? "—"}
        unit={active === null ? undefined : t("firewall.glance_rules_active")}
        foot={
          rules === null ? (
            "—"
          ) : (
            <>
              <Pill tone="ok" small>
                {counted(t, "firewall.rules_pass", rules.pass)}
              </Pill>{" "}
              <Pill tone="danger" small>
                {counted(t, "firewall.rules_block", rules.block)}
              </Pill>{" "}
              {rules.off > 0 && (
                <Pill tone="neutral" small>
                  {counted(t, "firewall.rules_off", rules.off)}
                </Pill>
              )}
            </>
          )
        }
        to="#regles"
      />
    </div>
  );
}

// Les passerelles, chacune avec son état, son adresse et ce que le pare-feu en mesure.
export function FirewallGateways({ gateways }: { gateways: FirewallGateway[] }) {
  const t = useT();
  const { language } = useI18n();
  return (
    <div id="passerelles">
      <Card>
        <CardHeader title={t("firewall.gateways_title")} aside={gateways.length} />
        <ul className="firewall-gateways">
          {gateways.map((gateway) => (
            <li key={gateway.name} className="firewall-gateway">
              <Dot tone={gatewayTone(gateway.status)} />
              <b>{gateway.name}</b>
              <Pill tone={gatewayTone(gateway.status)} small dot={false}>
                {gatewayStatusLabel(t, gateway.status)}
              </Pill>
              <span className="mono secondary">
                {gateway.address}
                {gateway.delay_ms === null ? ` · ${t("firewall.gateways_unmonitored")}` : ` · ${gatewayDelay(gateway, language)} · ${gatewayLoss(gateway, language)}`}
              </span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
