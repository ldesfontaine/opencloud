import { Outlet, useLocation, useNavigate, useParams } from "react-router";

import { removeFirewall, useFirewall, useFirewallFlows, type FirewallDetail } from "../api/firewalls";
import { Button } from "../components/Button";
import { ConfirmInline } from "../components/ConfirmInline";
import { Failure } from "../components/Failure";
import { PageHead } from "../components/PageHead";
import { TabNav } from "../components/TabNav";
import { useAction } from "../hooks/useAction";
import { useT } from "../i18n/context";
import { FirewallFlows } from "./firewalls/FirewallFlows";
import { FirewallOverview } from "./firewalls/FirewallOverview";
import { NotFound } from "./NotFound";
import "./Firewall.scss";

// La fiche d'un pare-feu en deux onglets : la vue d'ensemble (l'état, les
// interfaces, les règles, les baux) et « qui parle à qui » (la grille des
// zones). Elle montre ce que son dernier relevé réussi a dit. Lecture seule :
// rien n'est écrit dans le pare-feu.
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
  const isFlows = useLocation().pathname.endsWith("/flux");
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
      <TabNav
        tabs={[
          { to: `/pare-feu/${firewall.id}`, key: "firewall.tab_overview" },
          { to: `/pare-feu/${firewall.id}/flux`, key: "firewall.tab_flows" },
        ]}
      />
      {isFlows ? <FlowsTab firewall={firewall} /> : <FirewallOverview firewall={firewall} />}
      {/* La fenêtre de l'adresse : modifier le pare-feu. */}
      <Outlet />
    </>
  );
}

// L'onglet « qui parle à qui » ne lit la grille que lorsqu'on l'ouvre.
function FlowsTab({ firewall }: { firewall: FirewallDetail }) {
  const flows = useFirewallFlows(firewall.id);
  return (
    <div className="firewall">
      <Failure error={flows.error} />
      {flows.data && <FirewallFlows flows={flows.data} />}
    </div>
  );
}
