import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";

import { api, errorKey } from "../api/client";
import type { TokenResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Field, Input } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { useT } from "../i18n/context";
import { useRefresh } from "../lib/refresh";

// L'opérateur nomme la machine ; le jeton naît et s'affiche une seule fois,
// sur la page suivante, avec la commande à coller.
export function MachineNew() {
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const [name, setName] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    api
      .post<TokenResponse>("/api/machines/tokens", { name })
      .then((token) => {
        refresh();
        void navigate("/machines/jeton", { state: token });
      })
      .catch((failure: unknown) => setError(errorKey(failure)))
      .finally(() => setBusy(false));
  };

  return (
    <>
      <PageHead title={t("machine.new_title")} subtitle={t("machine.new_subtitle")} />
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
          <Field label={t("machine.name_label")} htmlFor="machine-name" help={t("machine.name_help")} {...(error ? { error: t(error) } : {})}>
            <Input
              id="machine-name"
              type="text"
              value={name}
              onChange={(event) => setName(event.target.value)}
              placeholder="vps-paris-1"
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <div className="cluster">
            <Button type="submit" variant="primary" icon="plus" disabled={busy}>
              {t("machine.create_token")}
            </Button>
            <Button variant="ghost" to="/machines">
              {t("machine.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
}
