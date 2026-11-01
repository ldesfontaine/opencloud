import type { FirewallDetail } from "../../api/firewalls";
import { CardBody, KeyValue } from "../../components/Card";
import { Disclosure } from "../../components/Disclosure";
import { Pill } from "../../components/Pill";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { firewallStateLabel, firewallTone } from "../../lib/firewalls";
import { formatWhen } from "../../lib/time";

// La connexion, repliée : ce qui a servi à la déclarer. La clé n'y figure
// jamais en entier, seulement ses quatre premiers caractères ; le secret, jamais.
export function FirewallConnection({ firewall }: { firewall: FirewallDetail }) {
  const t = useT();
  const now = useNow();
  return (
    <div id="connexion">
      <Disclosure
        title={t("firewall.card_title")}
        initiallyOpen={false}
        aside={
          <Pill tone={firewallTone(firewall)} small>
            {firewallStateLabel(t, firewall)}
          </Pill>
        }
      >
        <CardBody gap={10}>
          <KeyValue label={t("firewall.field_connector")} value={t(`firewall.kind_${firewall.kind}`)} />
          <KeyValue label={t("firewall.field_api")} value={firewall.api_url} mono />
          <KeyValue label={t("firewall.field_version")} value={firewall.version === "" ? "—" : firewall.version} mono />
          <KeyValue label={t("firewall.field_key")} value={firewall.api_key_hint} mono />
          <KeyValue label={t("firewall.field_read")} value={firewall.last_read_at === null ? t("firewall.never_read") : formatWhen(firewall.last_read_at, now)} />
          <KeyValue label={t("firewall.field_certificate")} value={firewall.is_pinned ? t("firewall.pinned") : t("firewall.system_trust")} />
        </CardBody>
      </Disclosure>
    </div>
  );
}
