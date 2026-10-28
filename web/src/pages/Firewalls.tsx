import { useFirewalls, type Firewall } from "../api/firewalls";
import { Button } from "../components/Button";
import { Card, EmptyCard } from "../components/Card";
import { CardGrid, EntityCard } from "../components/EntityCard";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { RowLink, Table } from "../components/Table";
import { ViewToggle } from "../components/ViewToggle";
import { useNow } from "../hooks/useNow";
import { useViewMode } from "../hooks/useViewMode";
import { useT } from "../i18n/context";
import { firewallStateLabel, firewallTone, shortAddress } from "../lib/firewalls";
import { formatWhen } from "../lib/time";

// La liste des pare-feu déclarés, en cartes ou en tableau. Lecture seule :
// rien n'est écrit chez eux. Sans pare-feu, la page dit comment en ajouter un.
export function Firewalls() {
  const t = useT();
  const firewalls = useFirewalls();
  const [mode, setMode] = useViewMode("firewalls", "cards");
  const list = firewalls.data?.firewalls ?? [];
  const isEmpty = firewalls.data !== null && list.length === 0;
  return (
    <>
      <PageHead
        title={t("firewall.title")}
        subtitle={t("firewall.subtitle")}
        actions={
          <>
            {list.length > 1 && <ViewToggle mode={mode} onChange={setMode} />}
            <Button variant="primary" icon="plus" to="/pare-feu/nouveau">
              {t("firewall.declare")}
            </Button>
          </>
        }
      />
      {firewalls.error && <Failure error={firewalls.error} />}
      {isEmpty && <NoFirewall />}
      {list.length > 0 && (mode === "cards" || list.length === 1 ? <FirewallCards list={list} /> : <FirewallTable list={list} />)}
    </>
  );
}

// Ce que la page dit tant qu'aucun pare-feu n'est déclaré : comment en
// ajouter un, et rien d'autre.
function NoFirewall() {
  const t = useT();
  return (
    <EmptyCard
      icon="firewall"
      text={t("firewall.none_text")}
      action={
        <Button icon="plus" to="/pare-feu/nouveau">
          {t("firewall.declare")}
        </Button>
      }
    >
      <div className="card-b gap-8">
        <p className="secondary">{t("firewall.none_howto")}</p>
        <p className="secondary">{t("firewall.rights_text")}</p>
        <p className="secondary">{t("firewall.rights_cost")}</p>
      </div>
    </EmptyCard>
  );
}

function FirewallCards({ list }: { list: Firewall[] }) {
  const t = useT();
  const now = useNow();
  return (
    <CardGrid>
      {list.map((firewall) => (
        <EntityCard
          key={firewall.id}
          to={`/pare-feu/${firewall.id}`}
          icon="firewall"
          name={firewall.name}
          kind={t(`firewall.kind_${firewall.kind}`)}
          state={
            <Pill tone={firewallTone(firewall)} small>
              {firewallStateLabel(t, firewall)}
            </Pill>
          }
          facts={[
            { label: t("firewall.fact_address"), value: shortAddress(firewall.api_url) },
            { label: t("firewall.fact_version"), value: firewall.version === "" ? "—" : firewall.version },
            { label: t("firewall.fact_rules"), value: firewall.counts.rules },
            { label: t("firewall.fact_leases"), value: firewall.counts.leases },
            { label: t("firewall.fact_read"), value: firewall.last_read_at === null ? "—" : formatWhen(firewall.last_read_at, now), mono: false },
          ]}
          actions={[{ label: t("firewall.edit"), to: `/pare-feu/${firewall.id}/modifier` }]}
        />
      ))}
    </CardGrid>
  );
}

function FirewallTable({ list }: { list: Firewall[] }) {
  const t = useT();
  const now = useNow();
  return (
    <Card>
      <Table fit>
        <thead>
          <tr>
            <th>{t("firewall.col_name")}</th>
            <th>{t("firewall.col_state")}</th>
            <th className="wide-only">{t("firewall.fact_address")}</th>
            <th className="wide-only">{t("firewall.fact_version")}</th>
            <th className="wide-only">{t("firewall.fact_rules")}</th>
            <th className="wide-only">{t("firewall.fact_leases")}</th>
            <th className="wide-only">{t("firewall.fact_read")}</th>
          </tr>
        </thead>
        <tbody>
          {list.map((firewall) => (
            <tr key={firewall.id}>
              <td>
                <RowLink to={`/pare-feu/${firewall.id}`} icon="firewall" actions={[{ label: t("firewall.edit"), to: `/pare-feu/${firewall.id}/modifier` }]}>
                  {firewall.name}
                </RowLink>
              </td>
              <td>
                <Pill tone={firewallTone(firewall)}>{firewallStateLabel(t, firewall)}</Pill>
              </td>
              <td className="num wide-only">{shortAddress(firewall.api_url)}</td>
              <td className="num wide-only">{firewall.version === "" ? "—" : firewall.version}</td>
              <td className="num wide-only">{firewall.counts.rules}</td>
              <td className="num wide-only">{firewall.counts.leases}</td>
              <td className="secondary nowrap wide-only">{firewall.last_read_at === null ? "—" : formatWhen(firewall.last_read_at, now)}</td>
            </tr>
          ))}
        </tbody>
      </Table>
    </Card>
  );
}
