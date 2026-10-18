import { useEffect, useState, type FormEvent } from "react";
import { useNavigate, useParams } from "react-router";

import { api, errorKey } from "../api/client";
import type { Capability, DeclaredService, MachinesResponse, Nature, Origin, ProjectsResponse } from "../api/types";
import { Button } from "../components/Button";
import { Card } from "../components/Card";
import { Failure } from "../components/Failure";
import { Field, Input, Select } from "../components/Field";
import { PageHead } from "../components/PageHead";
import { Pill } from "../components/Pill";
import { useResource } from "../hooks/useResource";
import { useT } from "../i18n/context";
import {
  capabilities,
  emptyServiceForm,
  emptyUnitForm,
  natures,
  origins,
  serviceFormOf,
  serviceRequestOf,
  toggled,
  type ServiceFormValues,
  type UnitFormValues,
} from "../lib/directory";
import { useRefresh } from "../lib/refresh";
import { NotFound } from "./NotFound";
import "./StatusAdmin.scss";
import "./DeclaredServiceForm.scss";

// Déclarer ou modifier un service : l'opérateur dit sur quelle machine il
// tourne, de quelle nature et d'où, ce qu'il fournit s'il sert les autres, et
// ses unités. En édition, le formulaire part du service et le remplace
// entier ; une unité gardée garde son identifiant, une unité retirée est
// supprimée. L'appartenance aux projets se règle ici, depuis le service.
// Le serveur valide et répond par la clé du refus.
export function DeclaredServiceForm() {
  const { id } = useParams();
  const isEditing = id !== undefined;
  const t = useT();
  const navigate = useNavigate();
  const { refresh } = useRefresh();
  const machines = useResource<MachinesResponse>("/api/machines");
  const projects = useResource<ProjectsResponse>("/api/directory/projects");
  const existing = useResource<DeclaredService>(isEditing ? `/api/directory/services/${id}` : null);

  const [values, setValues] = useState<ServiceFormValues>(emptyServiceForm);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  // Le service se lit une fois : une relecture (le signal de rafraîchissement
  // passe) ne doit pas écraser ce que l'opérateur est en train de taper.
  const [filled, setFilled] = useState(false);

  useEffect(() => {
    if (filled || existing.data === null) {
      return;
    }
    setFilled(true);
    setValues(serviceFormOf(existing.data));
  }, [existing.data, filled]);

  // À défaut de choix, la machine openCloud : c'est celle qu'on a sous la main.
  const local = machines.data?.machines.find((machine) => machine.local);
  useEffect(() => {
    if (!isEditing && values.machineID === "" && local !== undefined) {
      setValues((current) => ({ ...current, machineID: local.id }));
    }
  }, [isEditing, local, values.machineID]);

  if (isEditing && existing.error?.status === 404) {
    return <NotFound />;
  }

  const change = <Key extends keyof ServiceFormValues>(key: Key, value: ServiceFormValues[Key]) => {
    setValues((current) => ({ ...current, [key]: value }));
  };
  const changeUnit = <Key extends keyof UnitFormValues>(index: number, key: Key, value: UnitFormValues[Key]) => {
    setValues((current) => ({
      ...current,
      units: current.units.map((unit, position) => (position === index ? { ...unit, [key]: value } : unit)),
    }));
  };
  const addUnit = () => setValues((current) => ({ ...current, units: [...current.units, emptyUnitForm()] }));
  const removeUnit = (index: number) => {
    setValues((current) => ({ ...current, units: current.units.filter((_, position) => position !== index) }));
  };

  const submit = (event: FormEvent) => {
    event.preventDefault();
    setBusy(true);
    const body = serviceRequestOf(values);
    const request = isEditing ? api.put<DeclaredService>(`/api/directory/services/${id}`, body) : api.post<DeclaredService>("/api/directory/services", body);
    request
      .then((saved) => {
        refresh("directory");
        void navigate(`/services/annuaire/${saved.id}`);
      })
      .catch((failed: unknown) => setError(errorKey(failed)))
      .finally(() => setBusy(false));
  };

  const allProjects = projects.data?.projects ?? [];
  return (
    <>
      <PageHead
        title={isEditing ? t("directory.edit_title") : t("directory.new_title")}
        subtitle={isEditing ? t("directory.edit_subtitle") : t("directory.new_subtitle")}
      />
      {existing.error && <Failure error={existing.error} />}
      <Card className="form-card">
        <form className="card-b" onSubmit={submit}>
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
            <Select id="service-machine" icon="server" value={values.machineID} onChange={(event) => change("machineID", event.target.value)}>
              {(machines.data?.machines ?? []).map((machine) => (
                <option key={machine.id} value={machine.id}>
                  {machine.name}
                </option>
              ))}
            </Select>
          </Field>
          <div className="grid-2">
            <Field label={t("directory.nature_label")} htmlFor="service-nature" help={t("directory.nature_help")}>
              <Select id="service-nature" value={values.nature} onChange={(event) => change("nature", event.target.value as Nature)}>
                {natures.map((nature) => (
                  <option key={nature} value={nature}>
                    {t(`directory.nature_${nature}`)}
                  </option>
                ))}
              </Select>
            </Field>
            <Field label={t("directory.origin_label")} htmlFor="service-origin" help={t("directory.origin_help")}>
              <Select id="service-origin" value={values.origin} onChange={(event) => change("origin", event.target.value as Origin)}>
                {origins.map((origin) => (
                  <option key={origin} value={origin}>
                    {t(`directory.origin_${origin}`)}
                  </option>
                ))}
              </Select>
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
              <div className="checks">
                {capabilities.map((capability: Capability) => (
                  <label key={capability}>
                    <input type="checkbox" checked={values.provides.includes(capability)} onChange={() => change("provides", toggled(values.provides, capability))} />
                    {t(`directory.provides_${capability}`)}
                  </label>
                ))}
              </div>
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
              <div className="checks">
                {allProjects.map((project) => (
                  <label key={project.id}>
                    <input type="checkbox" checked={values.projectIDs.includes(project.id)} onChange={() => change("projectIDs", toggled(values.projectIDs, project.id))} />
                    {project.name}
                  </label>
                ))}
              </div>
            )}
          </Field>
          {error !== null && <Pill tone="danger">{t(error)}</Pill>}
          <div className="cluster">
            <Button type="submit" variant="primary" icon={isEditing ? "check" : "plus"} disabled={busy || (isEditing && !filled)}>
              {isEditing ? t("directory.save") : t("directory.create")}
            </Button>
            <Button variant="ghost" to={isEditing ? `/services/annuaire/${id}` : "/services/annuaire"}>
              {t("directory.cancel")}
            </Button>
          </div>
        </form>
      </Card>
    </>
  );
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
        <Field label={t("directory.unit_digest_label")} htmlFor={`${prefix}-digest`} help={t("directory.unit_digest_help")}>
          <Input id={`${prefix}-digest`} mono type="text" value={unit.digest} onChange={(event) => change("digest", event.target.value)} maxLength={71} autoComplete="off" />
        </Field>
      )}
    </div>
  );
}
