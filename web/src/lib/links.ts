import type { LinkEnd, LinkProof, LinkRequest, ServiceLink } from "../api/links";
import type { DeclaredService, ServiceUnit } from "../api/directory";
import type { Translate } from "../i18n/context";
import { ago } from "./time";
import type { Tone } from "./tone";
import type { SelectOption } from "./select";
import { unitKeyOf } from "./directory";

// La teinte de la preuve : un mot toujours, la couleur n'est qu'un renfort.
// Déclaré se tait ; déduit et observé sont des affirmations à confirmer.
export function proofTone(proof: LinkProof): Tone {
  return proof === "declared" ? "ok" : "accent";
}

// Le nom d'une extrémité : celui du service déclaré qui la porte, sinon le
// nom de l'instance.
export function endName(end: Pick<LinkEnd, "service_name" | "label">): string {
  return end.service_name !== "" ? end.service_name : end.label;
}

// Un lien déduit que l'opérateur n'a pas encore décidé : on lui propose de le
// confirmer ou de l'écarter.
export function isProposed(link: Pick<ServiceLink, "proof" | "dismissed">): boolean {
  return link.proof === "deduced" && !link.dismissed;
}

// La fraîcheur d'un lien, en mots : « Vu il y a 2 h », « Écrit le … » pour
// celui que l'opérateur a écrit (personne ne le « revoit »), « Calculé à
// l'instant » pour un lien déduit (refait à chaque lecture), « Écartée le … ».
// Un lien ancien le dit en plus (stale).
export function freshnessText(t: Translate, link: Pick<ServiceLink, "proof" | "dismissed" | "signal" | "first_seen_at" | "last_seen_at">, now: number): string {
  if (link.dismissed) {
    return t("link.dismissed_on", link.first_seen_at.slice(0, 10));
  }
  if (link.proof === "deduced") {
    return t("link.deduced_fresh");
  }
  if (link.signal === "operator") {
    return t("link.written", link.first_seen_at.slice(0, 10));
  }
  return t("link.seen", ago(t, link.last_seen_at, now));
}


export type LinkDirection = "outgoing" | "incoming";

export interface LinkFormValues {
  direction: LinkDirection;
  peerServiceID: string;
  // Vide : l'unique unité, ou à choisir si le service en a plusieurs.
  selfUnitID: string;
  peerUnitID: string;
  port: string;
}

export function emptyLinkForm(): LinkFormValues {
  return { direction: "outgoing", peerServiceID: "", selfUnitID: "", peerUnitID: "", port: "" };
}

// Les services qu'on peut relier à celui-ci : tous les autres, par machine.
export function peerOptions(services: readonly DeclaredService[], selfID: string): SelectOption[] {
  return services
    .filter((candidate) => candidate.id !== selfID)
    .map((candidate) => ({ value: candidate.id, label: candidate.name, hint: candidate.machine_name }))
    .sort((a, b) => a.hint.localeCompare(b.hint) || a.label.localeCompare(b.label));
}

// Les unités d'un service à choisir, rien s'il n'en a qu'une : elle se choisit seule.
export function unitOptions(service: Pick<DeclaredService, "origin" | "units"> | undefined): SelectOption[] {
  if (service === undefined || service.units.length < 2) {
    return [];
  }
  return service.units.map((unit: ServiceUnit) => ({ value: unit.id, label: unit.name, hint: unitKeyOf(unit, service.origin) }));
}

// Le formulaire est-il complet ? Un service en face, l'unité de chaque côté
// qui en a plusieurs, un port vide ou entre 1 et 65535.
export function isLinkComplete(values: LinkFormValues, self: Pick<DeclaredService, "origin" | "units">, peer: Pick<DeclaredService, "origin" | "units"> | undefined): boolean {
  if (peer === undefined) {
    return false;
  }
  const needs = (service: Pick<DeclaredService, "units">, unitID: string) => service.units.length < 2 || unitID !== "";
  return needs(self, values.selfUnitID) && needs(peer, values.peerUnitID) && isPortValid(values.port);
}

export function isPortValid(port: string): boolean {
  if (port.trim() === "") {
    return true;
  }
  const value = Number(port);
  return Number.isInteger(value) && value >= 1 && value <= 65535;
}

// Le corps de la requête : « utilise » part de ce service, « est utilisé par »
// y arrive.
export function linkRequestOf(selfID: string, values: LinkFormValues): LinkRequest {
  const port = values.port.trim() === "" ? 0 : Number(values.port);
  if (values.direction === "outgoing") {
    return { from_service_id: selfID, from_unit_id: values.selfUnitID, to_service_id: values.peerServiceID, to_unit_id: values.peerUnitID, port };
  }
  return { from_service_id: values.peerServiceID, from_unit_id: values.peerUnitID, to_service_id: selfID, to_unit_id: values.selfUnitID, port };
}
