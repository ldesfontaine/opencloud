import { useState } from "react";

import { saveProject } from "../../api/directory";
import { Button } from "../../components/Button";
import { Failure } from "../../components/Failure";
import { Field, Input } from "../../components/Field";
import { useAction } from "../../hooks/useAction";
import { useT } from "../../i18n/context";

// Créer un projet sans quitter l'assistant d'un service : le nom, un bouton,
// et le projet est créé puis coché. Sans cela, l'étape « Projets » finissait
// sur une impasse et un assistant à moitié rempli. Entrée crée le projet : elle
// ne passe pas à l'étape suivante.
export function NewProject({ onCreated }: { onCreated: (projectID: string) => void }) {
  const t = useT();
  const action = useAction();
  const [name, setName] = useState("");
  const create = () => {
    if (name.trim() === "") {
      return;
    }
    action.run(() => saveProject(undefined, { name: name.trim(), title: "", publication: null }), {
      topic: "directory",
      onDone: (project) => {
        setName("");
        onCreated(project.id);
      },
    });
  };
  return (
    <div className="new-project">
      <Field label={t("directory.projects_new_label")} htmlFor="service-new-project">
        <span className="cluster">
          <Input
            id="service-new-project"
            mono
            type="text"
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") {
                event.preventDefault();
                create();
              }
            }}
            placeholder={t("directory.projects_new_placeholder")}
            maxLength={63}
            autoComplete="off"
          />
          <Button icon="plus" disabled={action.busy || name.trim() === ""} onClick={create}>
            {t("directory.projects_new_create")}
          </Button>
        </span>
      </Field>
      <Failure error={action.error} />
    </div>
  );
}
