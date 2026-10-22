import { describe, expect, it } from "vitest";

import type { Probe, ProbeDay } from "../api/probes";
import { dayKeys, dayTone, editProbePath, emptyProbeForm, newProbePath, probeFormOf, probeListPath, probeNeedsAttention, probePath, probeRequestOf, probeSuggestionOf, renamedProbePath, uptimeOfDays, uptimePercent } from "./probes";

function day(date: string, total: number, success: number, degraded = 0): ProbeDay {
  return { day: date, total, success, degraded, duration_ms: 12 };
}

describe("probeNeedsAttention", () => {
  it("ne compte que la sonde hors ligne", () => {
    expect(probeNeedsAttention({ status: "down" })).toBe(true);
    expect(probeNeedsAttention({ status: "degraded" })).toBe(false);
    expect(probeNeedsAttention({ status: "paused" })).toBe(false);
  });
});

describe("uptimePercent", () => {
  it("rend rien plutôt qu'un zéro quand aucun essai n'est tombé", () => {
    expect(uptimePercent(0, 0)).toBeNull();
  });

  it("compte les succès sur les essais", () => {
    expect(uptimePercent(200, 199)).toBeCloseTo(99.5);
  });
});

describe("uptimeOfDays", () => {
  it("additionne les jours, parce que l'agrégat garde des comptes", () => {
    expect(uptimeOfDays([day("2026-09-17", 100, 100), day("2026-09-18", 100, 90)])).toEqual({ total: 200, success: 190 });
  });

  it("compte aussi les jours d'un composant de la page de statut, et rien sans jour", () => {
    const days = [
      { day: "2026-09-19", total: 10, success: 10, degraded: 0 },
      { day: "2026-09-20", total: 10, success: 7, degraded: 1 },
    ];
    expect(uptimeOfDays(days)).toEqual({ total: 20, success: 17 });
    expect(uptimeOfDays([])).toEqual({ total: 0, success: 0 });
  });
});

describe("dayTone", () => {
  it("laisse gris un jour sans essai", () => {
    expect(dayTone(undefined)).toBe("none");
    expect(dayTone(day("2026-09-18", 0, 0))).toBe("none");
  });

  it("met en attention un jour entièrement dégradé", () => {
    expect(dayTone(day("2026-09-18", 10, 10, 10))).toBe("warn");
  });

  it("met en danger dès un échec", () => {
    expect(dayTone(day("2026-09-18", 10, 9))).toBe("danger");
  });

  it("laisse au vert un jour sans échec ni dégradation", () => {
    expect(dayTone(day("2026-09-18", 10, 10))).toBe("ok");
  });
});

describe("dayKeys", () => {
  it("rend les jours UTC du plus ancien à aujourd'hui", () => {
    const now = Date.parse("2026-09-19T23:30:00Z");
    expect(dayKeys(3, now)).toEqual(["2026-09-17", "2026-09-18", "2026-09-19"]);
  });

  it("traverse un changement de mois", () => {
    const now = Date.parse("2026-10-01T00:10:00Z");
    expect(dayKeys(2, now)).toEqual(["2026-09-30", "2026-10-01"]);
  });
});

const httpProbe: Probe = {
  id: "a1b2c3d4e5f60718",
  name: "site nextcloud",
  kind: "http",
  target: "https://cloud.exemple.fr/",
  machine_id: "machine-1",
  machine_name: "vps",
  machine_online: true,
  service_id: "service-1",
  service_name: "nextcloud",
  service_machine_name: "vps",
  status: "up",
  interval_seconds: 120,
  timeout_seconds: 8,
  failure_threshold: 4,
  recovery_threshold: 3,
  method: "HEAD",
  expected_status: "200,301",
  expected_body: "bienvenue",
  follow_redirects: false,
  tls: false,
  last_checked_at: null,
  last_duration_ms: 0,
  last_code: null,
  last_reason: "",
  certificate: null,
  created_at: "2026-10-01T10:00:00Z",
};

describe("probeFormOf et probeRequestOf", () => {
  it("renvoient au serveur la sonde qu'ils ont lue, service compris", () => {
    expect(probeRequestOf(probeFormOf(httpProbe))).toEqual({
      name: "site nextcloud",
      kind: "http",
      target: "https://cloud.exemple.fr/",
      machine_id: "machine-1",
      service_id: "service-1",
      interval_seconds: 120,
      timeout_seconds: 8,
      failure_threshold: 4,
      recovery_threshold: 3,
      method: "HEAD",
      expected_status: "200,301",
      expected_body: "bienvenue",
      follow_redirects: false,
      tls: false,
    });
  });

  it("détache le service quand l'opérateur le retire", () => {
    expect(probeRequestOf({ ...probeFormOf(httpProbe), serviceID: "" }).service_id).toBe("");
  });

  it("envoie vides les champs HTTP d'une sonde TCP", () => {
    const tcp: Probe = { ...httpProbe, kind: "tcp", target: "10.8.0.2:5432", method: "", expected_status: "", expected_body: "", follow_redirects: false, tls: true };
    const form = probeFormOf(tcp);
    expect(form.method).toBe(emptyProbeForm.method);
    expect(probeRequestOf(form)).toMatchObject({
      kind: "tcp",
      method: "",
      expected_status: "",
      expected_body: "",
      follow_redirects: false,
      tls: true,
    });
  });

  it("ne retient pas la poignée de main d'une sonde HTTP", () => {
    const form = { ...probeFormOf(httpProbe), useTLS: "yes" as const };
    expect(probeRequestOf(form).tls).toBe(false);
  });
});

describe("probeSuggestionOf", () => {
  const service = { name: "nextcloud", title: "Nextcloud" };
  const port = { ip: "0.0.0.0", host_port: 8080, container_port: 80, protocol: "tcp" };

  it("prend le titre du service, son nom à défaut, et rien d'autre sans conteneur", () => {
    expect(probeSuggestionOf(service, undefined)).toEqual({ name: "Nextcloud" });
    expect(probeSuggestionOf({ ...service, title: "" }, undefined)).toEqual({ name: "nextcloud" });
  });

  it("sonde depuis la machine du conteneur, vers son premier port publié", () => {
    const carrier = { machine_id: "machine-2", ports: [port, { ...port, host_port: 8443 }] };
    expect(probeSuggestionOf(service, carrier)).toEqual({ name: "Nextcloud", machineID: "machine-2", target: "http://127.0.0.1:8080/" });
  });

  it("garde l'adresse d'un port publié sur une seule interface", () => {
    const carrier = { machine_id: "machine-2", ports: [{ ...port, ip: "10.8.0.2" }] };
    expect(probeSuggestionOf(service, carrier).target).toBe("http://10.8.0.2:8080/");
  });

  it("ne devine pas de cible sans port publié, mais garde la machine", () => {
    expect(probeSuggestionOf(service, { machine_id: "machine-2", ports: [] })).toEqual({ name: "Nextcloud", machineID: "machine-2" });
  });
});

describe("les adresses de la page Disponibilité", () => {
  it("un seul endroit les construit", () => {
    expect(probeListPath).toBe("/disponibilite");
    expect(probePath("p1")).toBe("/disponibilite/p1");
    expect(editProbePath("p1")).toBe("/disponibilite/p1/modifier");
    expect(newProbePath()).toBe("/disponibilite/nouvelle");
  });

  it("newProbePath garde ce qu'on sait déjà de la sonde, encodé", () => {
    expect(newProbePath({ service: "s1", container: "m1:abc" })).toBe("/disponibilite/nouvelle?service=s1&container=m1%3Aabc");
  });

  it("l'ancienne adresse /domaines/* mène à la nouvelle, le reste du chemin gardé", () => {
    expect(renamedProbePath("/domaines")).toBe("/disponibilite");
    expect(renamedProbePath("/domaines/nouvelle")).toBe("/disponibilite/nouvelle");
    expect(renamedProbePath("/domaines/p1")).toBe("/disponibilite/p1");
    expect(renamedProbePath("/domaines/p1/modifier")).toBe("/disponibilite/p1/modifier");
  });
});
