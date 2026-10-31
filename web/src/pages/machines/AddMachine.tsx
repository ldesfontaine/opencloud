import { useId, useState, type FormEvent } from "react";
import { useNavigate } from "react-router";

import { createMachineToken, type RoleID } from "../../api/machines";
import { Button } from "../../components/Button";
import { Fold } from "../../components/Disclosure";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { Select } from "../../components/Select";
import { Modal, ModalCancel } from "../../components/Modal";
import { RightsFields } from "../../components/RightsFields";
import { WizardBar } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useRoles, useT } from "../../i18n/context";
import { capabilitiesOf, emptyMachineForm, isMachineFormModified, rolePatch, tokenPageState, type TokenPageState } from "../../lib/machines";
import { EnrollmentToken } from "./EnrollmentToken";

// « Ajouter une machine » : une fenêtre à deux étapes sur la liste des
// machines, à l'adresse /machines/nouvelle (la liste est le fond, comme au
// rechargement). 1. nommer la machine et choisir son rôle, qui coche d'avance
// les droits de l'agent (sous « Avancé ») ;
// 2. la commande à coller dessus. Le jeton naît à la fin de la première
// étape et ne vit que dans cette fenêtre.
export function AddMachine() {
  const t = useT();
  const roles = useRoles();
  const navigate = useNavigate();
  const form = useForm(emptyMachineForm);
  const { values, change } = form;
  const action = useAction();
  const formID = useId();
  const [token, setToken] = useState<TokenPageState | null>(null);
  const close = () => void navigate("/machines", { replace: true });

  const create = (event: FormEvent) => {
    event.preventDefault();
    const rights = capabilitiesOf(values);
    action.run(() => createMachineToken(values.name, values.role, rights), {
      topic: "all",
      onDone: (created) => setToken(tokenPageState(created, rights)),
    });
  };

  const titles = [t("machine.step_name"), t("machine.step_install")];
  const bar = <WizardBar titles={titles} position={token === null ? 0 : 1} reached={token === null ? 0 : 1} canJump={false} onJump={() => undefined} />;

  if (token !== null) {
    return (
      <Modal
        title={t("machine.new_title")}
        onClose={close}
        wide
        actions={
          <Button variant="primary" onClick={close}>
            {t("modal.close")}
          </Button>
        }
      >
        {bar}
        <EnrollmentToken token={token} />
      </Modal>
    );
  }
  return (
    <Modal
      title={t("machine.new_title")}
      onClose={close}
      dirty={isMachineFormModified(values)}
      wide
      actions={
        <>
          <ModalCancel />
          <Button type="submit" form={formID} variant="primary" icon="plus" disabled={action.busy || values.name.trim() === ""}>
            {t("machine.create_token")}
          </Button>
        </>
      }
    >
      {bar}
      <form id={formID} className="modal-form" onSubmit={create}>
        <p className="secondary">{t("machine.new_subtitle")}</p>
        <Field label={t("machine.name_label")} htmlFor="machine-name" help={t("machine.name_help")}>
          <Input
            id="machine-name"
            type="text"
            value={values.name}
            onChange={(event) => change("name", event.target.value)}
            placeholder={t("machine.name_placeholder")}
            autoComplete="off"
            autoFocus
            required
          />
        </Field>
        <Field label={t("machine.role_label")} htmlFor="machine-role" help={t("machine.role_help")}>
          <Select
            id="machine-role"
            value={values.role}
            options={roles.map((offered) => ({ value: offered.id, label: t(`role.${offered.id}`) }))}
            onChange={(role) => form.update(rolePatch(role as RoleID, roles))}
          />
        </Field>
        <Fold label={t("machine.rights_advanced")} initiallyOpen={false}>
          {/* Le rôle Proxmox propose les droits proxmox.* : l'hôte n'est pas encore enrôlé, rien n'est détecté. */}
          <RightsFields values={values} update={form.update} proxmoxDetected={values.role === "proxmox"} />
        </Fold>
        <Failure error={action.error} />
      </form>
    </Modal>
  );
}
