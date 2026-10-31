import { useState } from "react";

import { setAPITokenReach, type APIToken, type ReachMachine } from "../../api/apiTokens";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { SettingBlock, SettingFoot } from "../../components/SettingRow";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";
import { hasProxmoxVerb, reachFormOf, reachRequestOf, type ReachFormValues } from "../../lib/apiTokens";
import { ReachChoices } from "./ReachChoices";

// Modifier la portée d'un jeton existant : le même choix qu'à la création,
// rempli de la portée actuelle. Le remplacement vaut aussitôt côté serveur.
// Route d'opérateur : un jeton ne la reçoit jamais.
export function ReachEditor({ token, machines, onDone, onCancel }: { token: APIToken; machines: ReachMachine[]; onDone: () => void; onCancel: () => void }) {
  const t = useT();
  const [values, setValues] = useState<ReachFormValues>(reachFormOf(token));
  const saving = useAction();
  const withGuests = hasProxmoxVerb(token.verbs);
  const request = reachRequestOf(values, withGuests);
  const save = () => {
    if (request !== null) {
      saving.run(() => setAPITokenReach(token.id, request), { onDone });
    }
  };
  return (
    <SettingBlock>
      <span className="card-t">{token.name}</span>
      <ReachChoices machines={machines} values={values} withGuests={withGuests} idPrefix={`reach-${token.id}`} onChange={(patch) => setValues({ ...values, ...patch })} />
      {saving.error !== null && <Failure error={saving.error} />}
      <SettingFoot>
        <Button type="button" variant="ghost" onClick={onCancel}>
          {t("api_token.reach_cancel")}
        </Button>
        <Button type="button" variant="primary" disabled={saving.busy || request === null} onClick={save}>
          {t("api_token.reach_save")}
        </Button>
      </SettingFoot>
    </SettingBlock>
  );
}
