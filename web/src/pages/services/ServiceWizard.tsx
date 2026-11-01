import { useEffect, useMemo } from "react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router";

import { saveDeclaredService, useDeclaredService, useProjects, useProposal, type Origin } from "../../api/directory";
import { useMachines } from "../../api/machines";
import { Button } from "../../components/Button";
import { Fold } from "../../components/Disclosure";
import { Failure } from "../../components/Failure";
import { Checkbox, Checks, Field, Input } from "../../components/Field";
import { MachineSelect } from "../../components/MachineSelect";
import { Modal } from "../../components/Modal";
import { EnumSelect } from "../../components/Select";
import { Wizard, type WizardStep } from "../../components/Wizard";
import { useAction } from "../../hooks/useAction";
import { useForm } from "../../hooks/useForm";
import { useT, type Translate } from "../../i18n/context";
import {
  capabilities,
  emptyServiceForm,
  emptyUnitForm,
  hasAdvancedUnitValues,
  natures,
  origins,
  serviceFormStart,
  serviceRequestOf,
  toggled,
  unitSummary,
  updatePolicies,
  type ServiceFormValues,
  type UnitFormValues,
} from "../../lib/directory";
import { isFormModified } from "../../lib/modal";
import "./ServiceWizard.scss";

// « Déclarer un service » : la fenêtre de la liste des services, à l'adresse
// /services/annuaire/nouveau, ou de la fiche du service, à
// /services/annuaire/:id/modifier. Trois étapes : l'identité (sur quelle
// machine il tourne, de quelle nature et d'où, ce qu'il fournit s'il sert les
// autres), ses unités (les réglages techniques sous « Avancé »), ses projets.
// En édition, l'assistant s'ouvre sur le récapitulatif et remplace le service
// entier ; une unité gardée garde son identifiant, une unité retirée est
// supprimée. Le serveur valide et répond par la clé du refus.
//
// Avec ?container=, l'assistant part de ce que le serveur propose pour ce
// conteneur : une déclaration nouvelle, ou, si son projet Compose est déjà
// déclaré, la modification de ce service, à laquelle les unités qui manquent
// s'ajoutent. C'est « Modifier avant d'accepter » ; « Accepter » écrit la même
// proposition sans passer par ici. Rien n'est écrit tant que l'opérateur n'a
// pas validé.
export function ServiceWizard() {
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
  // l'assistant ne partirait pas du bon côté. start ne change qu'avec ce
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

  const listPath = isEditing ? `/services/annuaire/${id}` : "/services";
  const close = () => void navigate(listPath, { replace: true });

  // Le projet Compose de ce conteneur est déjà déclaré : on y ajoute les
  // unités qui manquent plutôt que d'en déclarer un second.
  if (!isEditing && proposal.data !== null && proposal.data.service_id !== "") {
    return <Navigate replace to={`/services/annuaire/${proposal.data.service_id}/modifier?container=${encodeURIComponent(container)}`} />;
  }
  const accepting = container !== "" && proposal.error === null;
  // Une proposition refusée laisse un assistant vide, comme sans conteneur.
  const ready = form.isFilled || (!isEditing && !awaitingProposal);
  const base = start ?? { ...empty, machineID: first?.id ?? "" };

  const save = () => {
    const body = serviceRequestOf(values);
    action.run(() => saveDeclaredService(id, body), {
      topic: "directory",
      onDone: (saved) => void navigate(`/services/annuaire/${saved.id}`, { replace: true }),
    });
  };

  const header = heading(t, isEditing, accepting);
  return (
    <Modal title={header.title} onClose={close} dirty={ready && isFormModified(values, base)} wide>
      <p className="secondary">{header.subtitle}</p>
      {existing.error && <Failure error={existing.error} />}
      {proposal.error && <Failure error={proposal.error} />}
      {ready && (
        <Wizard
          steps={stepsOf(t, form, machines.data?.machines ?? [], projects.data?.projects ?? [])}
          mode={isEditing ? "edit" : "create"}
          onSubmit={save}
          submitLabel={isEditing ? t("directory.save") : t("directory.create")}
          submitIcon={isEditing ? "check" : "plus"}
          busy={action.busy}
          error={action.error}
          inModal
        />
      )}
    </Modal>
  );
}

// Le titre dit si l'on déclare, modifie, ou accepte (en le relisant) un conteneur que la
// machine montre.
function heading(t: Translate, isEditing: boolean, accepting: boolean) {
  if (accepting) {
    return isEditing
      ? { title: t("gap.accept_into_title"), subtitle: t("gap.accept_into_subtitle") }
      : { title: t("gap.accept_title"), subtitle: t("gap.accept_subtitle") };
  }
  return isEditing
    ? { title: t("directory.edit_title"), subtitle: t("directory.edit_subtitle") }
    : { title: t("directory.new_title"), subtitle: t("directory.new_subtitle") };
}

type ServiceForm = ReturnType<typeof useForm<ServiceFormValues, ServiceFormValues>>;

function stepsOf(t: Translate, form: ServiceForm, machines: { id: string; name: string }[], projects: { id: string; name: string }[]): WizardStep[] {
  const { values, change } = form;

  const changeUnit = <Key extends keyof UnitFormValues>(index: number, key: Key, value: UnitFormValues[Key]) => {
    change("units", values.units.map((unit, position) => (position === index ? { ...unit, [key]: value } : unit)));
  };
  const addUnit = () => change("units", [...values.units, emptyUnitForm()]);
  const removeUnit = (index: number) => {
    change("units", values.units.filter((_, position) => position !== index));
  };

  return [
    {
      id: "identity",
      title: t("directory.step_identity"),
      render: () => (
        <>
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
            <MachineSelect id="service-machine" icon="server" machines={machines} value={values.machineID} onChange={(machineID) => change("machineID", machineID)} />
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
        </>
      ),
      summary: () => [
        { label: t("directory.name_label"), value: values.name, mono: true },
        { label: t("directory.title_label"), value: values.title === "" ? "—" : values.title },
        { label: t("directory.machine_label"), value: machines.find((machine) => machine.id === values.machineID)?.name ?? "—" },
        { label: t("directory.nature_label"), value: t(`directory.nature_${values.nature}`) },
        { label: t("directory.origin_label"), value: values.origin === "compose" ? `${t("directory.origin_compose")} · ${values.composeProject}` : t(`directory.origin_${values.origin}`) },
        { label: t("directory.public_url_label"), value: values.publicURL === "" ? "—" : values.publicURL, mono: values.publicURL !== "" },
        ...(values.nature === "infra" ? [{ label: t("directory.provides_label"), value: values.provides.length === 0 ? "—" : values.provides.map((capability) => t(`directory.provides_${capability}`)).join(", ") }] : []),
      ],
    },
    {
      id: "units",
      title: t("directory.step_units"),
      render: () => (
        <>
          <p className="secondary">{t(values.origin === "compose" ? "directory.units_help_compose" : "directory.units_help")}</p>
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
        </>
      ),
      summary: () => values.units.map((unit, index) => ({ label: t("directory.unit_number", index + 1), value: unitSummary(unit, values.origin), mono: true })),
    },
    {
      id: "projects",
      title: t("directory.step_projects"),
      render: () => (
        <>
          <p className="secondary">{t("directory.projects_help")}</p>
          {projects.length === 0 ? (
            <span className="secondary">{t("directory.projects_none")}</span>
          ) : (
            <Checks>
              {projects.map((project) => (
                <Checkbox
                  key={project.id}
                  label={project.name}
                  checked={values.projectIDs.includes(project.id)}
                  onChange={() => change("projectIDs", toggled(values.projectIDs, project.id))}
                />
              ))}
            </Checks>
          )}
        </>
      ),
      summary: () => [
        {
          label: t("directory.projects_label"),
          value: values.projectIDs.length === 0 ? "—" : projects.filter((project) => values.projectIDs.includes(project.id)).map((project) => project.name).join(", "),
        },
      ],
    },
  ];
}

// Une unité : le champ qui la retrouve parmi ce que la machine montre
// dépend de l'origine du service, le conteneur ou l'unité systemd. Un
// binaire n'a pas d'image. Version, ports, dossier de données, empreinte et
// politique de mise à jour sont sous « Avancé ».
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
      <Fold label={t("wizard.advanced")} initiallyOpen={hasAdvancedUnitValues(unit)}>
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
          <>
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
            <div className="grid-2">
              <Field label={t("directory.unit_digest_label")} htmlFor={`${prefix}-digest`} help={t("directory.unit_digest_help")}>
                <Input id={`${prefix}-digest`} mono type="text" value={unit.digest} onChange={(event) => change("digest", event.target.value)} maxLength={71} autoComplete="off" />
              </Field>
              {/* La politique ne vaut que pour un conteneur : un binaire se suit. */}
              <Field label={t("directory.update_policy_label")} htmlFor={`${prefix}-policy`}>
                <EnumSelect id={`${prefix}-policy`} value={unit.updatePolicy} values={updatePolicies} labelKey="update.policy_" onChange={(policy) => change("updatePolicy", policy)} />
              </Field>
            </div>
          </>
        )}
      </Fold>
    </div>
  );
}
