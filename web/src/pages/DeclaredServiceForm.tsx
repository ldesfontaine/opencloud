import { useEffect, useMemo } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";

import { saveDeclaredService, useDeclaredService, useProjects, useProposal, type Origin } from "../api/directory";
import { useMachines } from "../api/machines";
import { Button } from "../components/Button";
import { Failure } from "../components/Failure";
import { Checkbox, Checks, Field, Input } from "../components/Field";
import { FormActions, FormCard } from "../components/FormCard";
import { FormPage } from "../components/FormPage";
import { MachineSelect } from "../components/MachineSelect";
import { PageHead } from "../components/PageHead";
import { EnumSelect } from "../components/Select";
import { useAction } from "../hooks/useAction";
import { useForm } from "../hooks/useForm";
import { useT, type Translate } from "../i18n/context";
import {
  capabilities,
  emptyServiceForm,
  emptyUnitForm,
  natures,
  origins,
  serviceFormStart,
  serviceRequestOf,
  toggled,
  updatePolicies,
  type ServiceFormValues,
  type UnitFormValues,
} from "../lib/directory";
import { NotFound } from "./NotFound";
import "./DeclaredServiceForm.scss";

// Déclarer ou modifier un service : l'opérateur dit sur quelle machine il
// tourne, de quelle nature et d'où, ce qu'il fournit s'il sert les autres, et
// ses unités. En édition, le formulaire part du service et le remplace
// entier ; une unité gardée garde son identifiant, une unité retirée est
// supprimée. L'appartenance aux projets se règle ici, depuis le service.
// Le serveur valide et répond par la clé du refus.
//
// Avec ?container=, le formulaire part de ce que le serveur propose pour
// adopter ce conteneur : une déclaration nouvelle, ou, si son projet Compose
// est déjà déclaré, la modification de ce service, à laquelle les unités qui
// manquent s'ajoutent. Rien n'est écrit tant que l'opérateur n'a pas validé.
export function DeclaredServiceForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const container = useSearchParams()[0].get("container") ?? "";
  const t = useT();
  const navigate = useNavigate();
  const machines = useMachines();
  const projects = useProjects();
  const existing = useDeclaredService(id ?? null);
  const proposal = useProposal(container === "" ? null : container);

  // Tant que la proposition n'est ni arrivée ni refusée, on ne remplit pas :
  // le formulaire ne partirait pas du bon côté. start ne change qu'avec ce
  // qu'il lit : l'effet de remplissage ne se relance pas à chaque rendu.
  const awaitingProposal = container !== "" && proposal.data === null && proposal.error === null;
  const empty = useMemo(emptyServiceForm, []);
  const start = useMemo(() => serviceFormStart(id, existing.data, proposal.data, awaitingProposal), [id, existing.data, proposal.data, awaitingProposal]);
  // start est déjà une valeur de formulaire (serviceFormStart), ou null pour attendre.
  const form = useForm(empty, isEditing || container !== "" ? { source: start, fill: (values: ServiceFormValues) => values } : null);
  const { values, change } = form;
  const action = useAction();

  // À défaut de choix, la première machine de la liste : le sélecteur n'a
  // pas d'option vide, il montrerait une machine sans l'avoir choisie.
  const first = machines.data?.machines[0];
  useEffect(() => {
    if (!isEditing && values.machineID === "" && first !== undefined) {
      change("machineID", first.id);
    }
  }, [isEditing, first, values.machineID]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }
  // Le projet Compose de ce conteneur est déjà déclaré : on y ajoute les
  // unités qui manquent plutôt que d'en déclarer un second.
  if (!isEditing && proposal.data !== null && proposal.data.service_id !== "") {
    return <Navigate replace to={`/services/annuaire/${proposal.data.service_id}/modifier?container=${encodeURIComponent(container)}`} />;
  }
  const waiting = !form.isFilled && (isEditing || awaitingProposal);
  const adopting = container !== "" && proposal.error === null;

  const changeUnit = <Key extends keyof UnitFormValues>(index: number, key: Key, value: UnitFormValues[Key]) => {
    change("units", values.units.map((unit, position) => (position === index ? { ...unit, [key]: value } : unit)));
  };
  const addUnit = () => change("units", [...values.units, emptyUnitForm()]);
  const removeUnit = (index: number) => {
    change("units", values.units.filter((_, position) => position !== index));
  };

  const save = () => {
    const body = serviceRequestOf(values);
    action.run(() => saveDeclaredService(id, body), {
      topic: "directory",
      onDone: (saved) => void navigate(`/services/annuaire/${saved.id}`),
    });
  };

  const allProjects = projects.data?.projects ?? [];
  return (
    <FormPage>
      <PageHead
        title={heading(t, isEditing, adopting).title}
        subtitle={heading(t, isEditing, adopting).subtitle}
      />
      {existing.error && <Failure error={existing.error} />}
      {proposal.error && <Failure error={proposal.error} />}
      <FormCard
        onSubmit={save}
        error={action.error}
        actions={
          <FormActions
            submitLabel={isEditing ? t("directory.save") : t("directory.create")}
            submitIcon={isEditing ? "check" : "plus"}
            busy={action.busy}
            canSubmit={!waiting}
            cancelLabel={t("directory.cancel")}
            cancelTo={isEditing ? `/services/annuaire/${id}` : "/services"}
          />
        }
      >
        <div className="grid-2">
          <Field label={t("directory.name_label")} htmlFor="service-name" help={t("directory.name_help")}>
            <Input
              id="service-name"
              mono
              type="text"
              value={values.name}
              onChange={(event) => change("name", event.target.value)}
              placeholder={t("directory.name_placeholder")}
              maxLength={63}
              autoComplete="off"
              autoFocus
              required
            />
          </Field>
          <Field label={t("directory.title_label")} htmlFor="service-title" help={t("directory.title_help")}>
            <Input id="service-title" type="text" value={values.title} onChange={(event) => change("title", event.target.value)} maxLength={80} autoComplete="off" />
          </Field>
        </div>
        <Field label={t("directory.machine_label")} htmlFor="service-machine" help={t("directory.machine_help")}>
          <MachineSelect
            id="service-machine"
            icon="server"
            machines={machines.data?.machines ?? []}
            value={values.machineID}
            onChange={(machineID) => change("machineID", machineID)}
          />
        </Field>
        <div className="grid-2">
          <Field label={t("directory.nature_label")} htmlFor="service-nature" help={t("directory.nature_help")}>
            <EnumSelect id="service-nature" value={values.nature} values={natures} labelKey="directory.nature_" onChange={(nature) => change("nature", nature)} />
          </Field>
          <Field label={t("directory.origin_label")} htmlFor="service-origin" help={t("directory.origin_help")}>
            <EnumSelect id="service-origin" value={values.origin} values={origins} labelKey="directory.origin_" onChange={(origin) => change("origin", origin)} />
          </Field>
        </div>
        {values.origin === "compose" && (
          <Field label={t("directory.compose_project_label")} htmlFor="service-compose" help={t("directory.compose_project_help")}>
            <Input
              id="service-compose"
              mono
              type="text"
              value={values.composeProject}
              onChange={(event) => change("composeProject", event.target.value)}
              placeholder={t("directory.compose_project_placeholder")}
              maxLength={63}
              autoComplete="off"
              required
            />
          </Field>
        )}
        <Field label={t("directory.public_url_label")} htmlFor="service-url" help={t("directory.public_url_help")}>
          <Input
            id="service-url"
            mono
            type="url"
            value={values.publicURL}
            onChange={(event) => change("publicURL", event.target.value)}
            placeholder={t("directory.public_url_placeholder")}
            maxLength={2048}
            autoComplete="off"
          />
        </Field>
        {values.nature === "infra" && (
          <Field label={t("directory.provides_label")} help={t("directory.provides_help")}>
            <Checks>
              {capabilities.map((capability) => (
                <Checkbox
                  key={capability}
                  label={t(`directory.provides_${capability}`)}
                  checked={values.provides.includes(capability)}
                  onChange={() => change("provides", toggled(values.provides, capability))}
                />
              ))}
            </Checks>
          </Field>
        )}
        <Field label={t("directory.units_label")} help={t(values.origin === "compose" ? "directory.units_help_compose" : "directory.units_help")}>
          <div className="units">
            {values.units.map((unit, index) => (
              <UnitEditor
                key={index}
                index={index}
                unit={unit}
                origin={values.origin}
                canRemove={values.units.length > 1}
                change={(key, value) => changeUnit(index, key, value)}
                remove={() => removeUnit(index)}
              />
            ))}
            <div className="cluster">
              <Button icon="plus" onClick={addUnit}>
                {t("directory.unit_add")}
              </Button>
            </div>
          </div>
        </Field>
        <Field label={t("directory.projects_label")} help={t("directory.projects_help")}>
          {allProjects.length === 0 ? (
            <span className="secondary">{t("directory.projects_none")}</span>
          ) : (
            <Checks>
              {allProjects.map((project) => (
                <Checkbox
                  key={project.id}
                  label={project.name}
                  checked={values.projectIDs.includes(project.id)}
                  onChange={() => change("projectIDs", toggled(values.projectIDs, project.id))}
                />
              ))}
            </Checks>
          )}
        </Field>
      </FormCard>
    </FormPage>
  );
}

// Le titre dit si l'on déclare, modifie, ou adopte un conteneur que la
// machine montre.
function heading(t: Translate, isEditing: boolean, adopting: boolean) {
  if (adopting) {
    return isEditing
      ? { title: t("gap.adopt_into_title"), subtitle: t("gap.adopt_into_subtitle") }
      : { title: t("gap.adopt_title"), subtitle: t("gap.adopt_subtitle") };
  }
  return isEditing
    ? { title: t("directory.edit_title"), subtitle: t("directory.edit_subtitle") }
    : { title: t("directory.new_title"), subtitle: t("directory.new_subtitle") };
}

// Une unité : le champ qui la retrouve parmi ce que la machine montre
// dépend de l'origine du service, le conteneur ou l'unité systemd. Un
// binaire n'a pas d'image.
function UnitEditor({
  index,
  unit,
  origin,
  canRemove,
  change,
  remove,
}: {
  index: number;
  unit: UnitFormValues;
  origin: Origin;
  canRemove: boolean;
  change: <Key extends keyof UnitFormValues>(key: Key, value: UnitFormValues[Key]) => void;
  remove: () => void;
}) {
  const t = useT();
  const prefix = `unit-${String(index)}`;
  return (
    <div className="unit-card">
      <div className="unit-head">
        <b>{t("directory.unit_number", index + 1)}</b>
        {canRemove && (
          <Button small variant="ghost" icon="x" onClick={remove}>
            {t("directory.unit_remove")}
          </Button>
        )}
      </div>
      <div className="grid-2">
        <Field label={t(origin === "compose" ? "directory.unit_name_compose_label" : "directory.unit_name_label")} htmlFor={`${prefix}-name`} help={t(origin === "compose" ? "directory.unit_name_compose_help" : "directory.unit_name_help")}>
          <Input id={`${prefix}-name`} mono type="text" value={unit.name} onChange={(event) => change("name", event.target.value)} maxLength={63} autoComplete="off" required />
        </Field>
        {origin !== "binary" && (
          <Field label={t("directory.unit_image_label")} htmlFor={`${prefix}-image`} help={t("directory.unit_image_help")}>
            <Input
              id={`${prefix}-image`}
              mono
              type="text"
              value={unit.image}
              onChange={(event) => change("image", event.target.value)}
              placeholder={t("directory.unit_image_placeholder")}
              maxLength={512}
              autoComplete="off"
              required
            />
          </Field>
        )}
        {origin === "container" && (
          <Field label={t("directory.unit_container_label")} htmlFor={`${prefix}-container`} help={t("directory.unit_container_help")}>
            <Input id={`${prefix}-container`} mono type="text" value={unit.containerName} onChange={(event) => change("containerName", event.target.value)} maxLength={255} autoComplete="off" required />
          </Field>
        )}
        {origin === "binary" && (
          <Field label={t("directory.unit_systemd_label")} htmlFor={`${prefix}-systemd`} help={t("directory.unit_systemd_help")}>
            <Input
              id={`${prefix}-systemd`}
              mono
              type="text"
              value={unit.systemdUnit}
              onChange={(event) => change("systemdUnit", event.target.value)}
              placeholder={t("directory.unit_systemd_placeholder")}
              maxLength={257}
              autoComplete="off"
              required
            />
          </Field>
        )}
      </div>
      <div className="grid-2">
        <Field label={t("directory.unit_version_label")} htmlFor={`${prefix}-version`} help={t("directory.unit_version_help")}>
          <Input id={`${prefix}-version`} mono type="text" value={unit.version} onChange={(event) => change("version", event.target.value)} maxLength={128} autoComplete="off" />
        </Field>
        <Field label={t("directory.unit_ports_label")} htmlFor={`${prefix}-ports`} help={t("directory.unit_ports_help")}>
          <Input
            id={`${prefix}-ports`}
            mono
            type="text"
            value={unit.ports}
            onChange={(event) => change("ports", event.target.value)}
            placeholder={t("directory.unit_ports_placeholder")}
            autoComplete="off"
          />
        </Field>
      </div>
      {origin !== "binary" && (
        <Field label={t("directory.data_path_label")} htmlFor={`${prefix}-data`} help={t("directory.data_path_help")}>
          <Input
            id={`${prefix}-data`}
            mono
            type="text"
            value={unit.dataPath}
            onChange={(event) => change("dataPath", event.target.value)}
            placeholder={t("directory.data_path_placeholder")}
            maxLength={255}
            autoComplete="off"
          />
        </Field>
      )}
      {origin !== "binary" && (
        <div className="grid-2">
          <Field label={t("directory.unit_digest_label")} htmlFor={`${prefix}-digest`} help={t("directory.unit_digest_help")}>
            <Input id={`${prefix}-digest`} mono type="text" value={unit.digest} onChange={(event) => change("digest", event.target.value)} maxLength={71} autoComplete="off" />
          </Field>
          {/* La politique ne vaut que pour un conteneur : un binaire se suit. */}
          <Field label={t("directory.update_policy_label")} htmlFor={`${prefix}-policy`}>
            <EnumSelect id={`${prefix}-policy`} value={unit.updatePolicy} values={updatePolicies} labelKey="update.policy_" onChange={(policy) => change("updatePolicy", policy)} />
          </Field>
        </div>
      )}
    </div>
  );
}
