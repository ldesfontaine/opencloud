import { useState } from "react";
import { useNavigate } from "react-router";

import { createKeyring, useBackups } from "../../api/backups";
import { ConfirmDialog } from "../../components/ConfirmModal";
import { Field, Input } from "../../components/Field";
import { ModalForm } from "../../components/ModalForm";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT } from "../../i18n/context";
import { backupsPath, passphraseProblem } from "../../lib/backups";

// « Créer la clé » ou « Remplacer la clé » : la fenêtre de la page Sauvegardes,
// à /sauvegardes/cle. La phrase de passe qui scelle la clé privée est demandée
// deux fois ; elle n'est lue qu'ici, le temps de créer la clé, et la fenêtre
// démontée ne la garde pas. La fenêtre attend le relevé : elle y lit si une
// clé existe déjà.
export function KeyringModal() {
  const backups = useBackups();
  return backups.data === null ? null : <KeyringDialog replacing={backups.data.keyring !== null} />;
}

function KeyringDialog({ replacing }: { replacing: boolean }) {
  const t = useT();
  const navigate = useNavigate();
  const form = useForm({ first: "", second: "" });
  const { values, change } = form;
  const action = useAction();
  const [refusal, setRefusal] = useState<string | null>(null);
  const [confirmingReplace, setConfirmingReplace] = useState(false);

  const close = () => void navigate(backupsPath, { replace: true });
  const createNow = () => {
    action.run(() => createKeyring(values.first, replacing), { topic: "backups", onDone: close });
  };
  const save = () => {
    const problem = passphraseProblem(values.first, values.second);
    setRefusal(problem);
    if (problem !== null) {
      return;
    }
    // Remplacer demande confirmation : les anciennes sauvegardes gardent
    // l'ancienne clé, et il faut garder l'ancienne phrase.
    if (replacing) {
      setConfirmingReplace(true);
      return;
    }
    createNow();
  };

  return (
    <>
      <ModalForm
        title={replacing ? t("backup.keyring_replace") : t("backup.keyring_create")}
        onClose={close}
        dirty={values.first !== "" || values.second !== ""}
        onSubmit={save}
        submitLabel={replacing ? t("backup.keyring_replace") : t("backup.keyring_create")}
        submitIcon="shield"
        busy={action.busy}
        error={refusal ?? action.error}
      >
        <Field label={t("backup.passphrase_label")} htmlFor="backup-passphrase" help={t("backup.passphrase_help")}>
          <Input id="backup-passphrase" type="password" autoComplete="new-password" maxLength={1024} value={values.first} onChange={(event) => change("first", event.target.value)} autoFocus required />
        </Field>
        <Field label={t("backup.passphrase_confirm_label")} htmlFor="backup-passphrase-confirm">
          <Input id="backup-passphrase-confirm" type="password" autoComplete="new-password" maxLength={1024} value={values.second} onChange={(event) => change("second", event.target.value)} required />
        </Field>
      </ModalForm>
      {confirmingReplace && (
        <ConfirmDialog
          title={t("backup.keyring_replace")}
          question={t("backup.keyring_replace_confirm")}
          confirmLabel={t("backup.keyring_replace")}
          onConfirm={createNow}
          onClose={() => setConfirmingReplace(false)}
        />
      )}
    </>
  );
}
