import { useState } from "react";

import { setMachineRole, type Machine, type RoleID } from "../api/machines";
import { useAction } from "../hooks/useAction";
import { useRoles, useT } from "../i18n/context";
import { Button } from "./Button";
import { Card, CardBody, CardHeader } from "./Card";
import { Failure } from "./Failure";
import { Select } from "./Select";

// Le rôle de la machine sur sa fiche : il décide de ses modules. Le changer ne
// touche pas aux droits de l'agent ; la carte le dit.
export function RoleCard({ machine }: { machine: Machine }) {
  const t = useT();
  const roles = useRoles();
  const action = useAction();
  const [chosen, setChosen] = useState<RoleID>(machine.role);
  return (
    <Card>
      <CardHeader title={t("machine.role_title")} />
      <CardBody gap={10}>
        <p className="secondary">{t("machine.role_text")}</p>
        <p className="secondary">{t(`role.${machine.role}_text`)}</p>
        <div className="role-row">
          <Select
            value={chosen}
            options={roles.map((offered) => ({ value: offered.id, label: t(`role.${offered.id}`) }))}
            onChange={(role) => setChosen(role as RoleID)}
            aria-label={t("machine.role_label")}
          />
          <Button small disabled={chosen === machine.role || action.busy} onClick={() => action.run(() => setMachineRole(machine.id, chosen), { topic: "machines" })}>
            {t("machine.role_set")}
          </Button>
        </div>
        <Failure error={action.error} />
      </CardBody>
    </Card>
  );
}
