import { expect, test } from "vitest";

import type { Counts, JobsResponse, ProbesResponse, ServicesResponse, Session } from "../api/types";
import countsGolden from "../../../internal/server/testdata/counts.golden.json";
import jobsGolden from "../../../internal/server/testdata/jobs.golden.json";
import probesGolden from "../../../internal/server/testdata/probes.golden.json";
import servicesGolden from "../../../internal/server/testdata/services.golden.json";
import sessionGolden from "../../../internal/server/testdata/session.golden.json";
import { read, soonest } from "./certificates";
import { attentionStatuses } from "./jobs";
import { needsAttention as probeNeedsAttention } from "./probes";
import { needsAttention as serviceNeedsAttention } from "./services";
import { updateAvailable } from "./updates";

// Les compteurs de la barre latérale et de la vue d'ensemble viennent du
// serveur ; les pastilles et les listes appliquent la même règle dans le
// navigateur, réécrite ici. Les fixtures figées de l'API, semées d'un même
// état, servent à vérifier que les deux comptent pareil : une règle qui
// bouge d'un seul côté casse ce test.
const counts = countsGolden as Counts;
const services = (servicesGolden as ServicesResponse).services;
const probes = (probesGolden as ProbesResponse).probes;
const jobs = (jobsGolden as JobsResponse).jobs;
const session = sessionGolden as Session;
// L'instant des fixtures : testNow de internal/server.
const now = Date.parse("2026-09-12T12:00:00Z");

test("services : ce que la pastille dit défaillant, la barre le compte", () => {
  expect(services.filter((service) => serviceNeedsAttention(service)).length).toBe(counts.services.attention);
  expect(services.length).toBe(counts.services.total);
});

test("mises à jour : ce que la pastille montre, la vue d'ensemble le compte", () => {
  expect(services.filter((service) => updateAvailable(service)).length).toBe(counts.services.updates);
});

test("tâches : ce que la liste met en danger, la barre le compte", () => {
  expect(jobs.filter((job) => attentionStatuses.has(job.status)).length).toBe(counts.jobs.attention);
  expect(jobs.length).toBe(counts.jobs.total);
});

test("sondes : ce que la liste dit hors ligne, la barre le compte", () => {
  expect(probes.filter((probe) => probeNeedsAttention(probe)).length).toBe(counts.probes.attention);
  expect(probes.length).toBe(counts.probes.total);
});

test("certificats : les échéances de l'onglet et de la vue d'ensemble se comptent pareil", () => {
  const seen = probes.filter((probe) => probe.certificate !== null && probe.status !== "paused");
  const readings = seen.map((probe) => read(probe.certificate!, session.certificate_thresholds, now));
  expect(seen.length).toBe(counts.certificates.total);
  expect(readings.filter((reading) => reading.state === "to_renew").length).toBe(counts.certificates.expiring);
  expect(readings.filter((reading) => reading.state === "expired").length).toBe(counts.certificates.expired);
  const alive = seen.filter((_, index) => readings[index]!.days >= 0).map((probe) => probe.certificate);
  expect(soonest(alive)?.not_after ?? null).toBe(counts.certificates.soonest_expires_at);
});
