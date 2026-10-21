import { changePassword, disableAuth, type AuthState } from "../../api/auth";
import { Button } from "../../components/Button";
import { Card, CardBody, CardHeader, KeyValue, Note } from "../../components/Card";
import { Failure } from "../../components/Failure";
import { Pill } from "../../components/Pill";
import { useAction } from "../../hooks/useAction";
import { useNow } from "../../hooks/useNow";
import { useT } from "../../i18n/context";
import { isForwardInsecure } from "../../lib/auth";
import { ago } from "../../lib/time";
import { AccessCredentialsForm } from "./AccessCredentialsForm";

function reloadPage() {
  window.location.reload();
}

// L'accès à l'interface, tel que la première configuration l'a posé.
// Changer le mot de passe rend caducs les identifiants du navigateur : la
// page se recharge, et le navigateur les demande dans sa propre fenêtre.
// Désactiver ramène l'installation à sa première configuration. En mode
// mandataire, il n'y a rien à saisir après coup : la carte montre l'en-tête
// et l'identité que le mandataire transmet.
export function AccessSection({ state }: { state: AuthState }) {
  const t = useT();
  const now = useNow();
  const action = useAction();
  const enabled = state.mode !== "none";

  const disable = () => {
    action.run(() => disableAuth(), { confirm: t("auth.disable_confirm"), onDone: reloadPage });
  };
  const changeTo = (password: string) => {
    action.run(() => changePassword(password), { onDone: reloadPage });
  };

  return (
    <Card className="form-card">
      <CardHeader title={t("auth.title")} aside={<Pill tone={enabled && !isForwardInsecure(state) ? "ok" : "warn"}>{t(`auth.state_${state.mode}`)}</Pill>} />
      <CardBody>
        <p>{t("auth.intro")}</p>
        {isForwardInsecure(state) && <Note tone="warn">{t("auth.forward_insecure")}</Note>}
        {state.mode === "basic" && <KeyValue label={t("auth.username_label")} value={state.username} mono />}
        {state.mode === "forward" && (
          <>
            <KeyValue label={t("auth.header_label")} value={state.header ?? ""} mono />
            <KeyValue label={t("auth.identity_label")} value={state.username} mono />
          </>
        )}
        {enabled && (
          <div className="cluster">
            <span className="secondary">{state.enabled_at !== null && t("auth.enabled_since", ago(t, state.enabled_at, now))}</span>
            <Button variant="danger" icon="x" onClick={disable} disabled={action.busy}>
              {t("auth.disable")}
            </Button>
          </div>
        )}
        <Failure error={action.error} />
      </CardBody>
      {state.mode === "basic" && (
        <AccessCredentialsForm withUsername={false} minLength={state.password_min_length} busy={action.busy} onSubmit={(_username, password) => changeTo(password)} />
      )}
    </Card>
  );
}
