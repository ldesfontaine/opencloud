import { expect, test } from "vitest";

import type { Job } from "../api/types";
import { awaitsFirstPing } from "./jobs";

const job: Job = {
  id: "a0dd0f6d04899b5d",
  name: "Sauvegarde restic nocturne",
  machine_id: "",
  machine_name: "",
  status: "new",
  interval_seconds: 86400,
  grace_seconds: 3600,
  last_ping_at: null,
  next_deadline_at: null,
  run_started_at: null,
  last_exit_code: null,
  last_duration_ms: null,
  created_at: "2026-10-12T07:44:57Z",
};

test("une tâche reste à installer tant qu'aucun ping n'est arrivé", () => {
  expect(awaitsFirstPing(job)).toBe(true);
});

test("une tâche est installée dès le premier ping, même en échec", () => {
  expect(awaitsFirstPing({ ...job, status: "failed", last_ping_at: "2026-10-12T07:50:08Z", last_exit_code: 1 })).toBe(false);
});
