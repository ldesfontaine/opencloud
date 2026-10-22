import { expect, test } from "vitest";

import type { ImageCheck } from "../api/containers";
import type { ContainerDirectory } from "../api/directory";
import type { Translate } from "../i18n/context";
import { checkText, imageLineChange, needsComposeEdit, shortDigest, updateAvailable, updateLabel, upgradeOffer, upgradeServiceID } from "./updates";

const t: Translate = (key, ...args) => [key, ...args].join(":");

function check(overrides: Partial<ImageCheck>): ImageCheck {
  return {
    checked_at: "2026-09-22T12:00:00Z", outcome: "ok", local_digest: "sha256:aaaa", remote_digest: "sha256:aaaa",
    newer_tag: "", newer_digest: "", kind: "", target: "", command: "", ...overrides,
  };
}

test("updateAvailable suit le constat et la politique", () => {
  const minor = check({ kind: "minor", newer_tag: "3.22", target: "alpine:3.22" });
  expect(updateAvailable({ update_policy: "watch", image_check: minor })).toBe(true);
  expect(updateAvailable({ update_policy: "hold", image_check: minor })).toBe(false);
  expect(updateAvailable({ update_policy: "ignore", image_check: minor })).toBe(true);
  expect(updateAvailable({ update_policy: "watch", image_check: check({}) })).toBe(false);
  expect(updateAvailable({ update_policy: "watch", image_check: null })).toBe(false);
});

test("updateLabel écrit le tag, ou « reconstruite »", () => {
  expect(updateLabel(t, check({ kind: "major", newer_tag: "17" }))).toBe("17");
  expect(updateLabel(t, check({ kind: "digest" }))).toBe("update.pill_digest");
});

test("checkText dit la mise à jour ou pourquoi il n'y en a pas", () => {
  expect(checkText(t, check({}))).toBe("update.up_to_date");
  expect(checkText(t, check({ kind: "patch", target: "nginx:1.27.1" }))).toBe("update.available_patch:nginx:1.27.1");
  expect(checkText(t, check({ outcome: "unauthorized" }))).toBe("update.outcome_unauthorized");
  expect(checkText(t, check({ outcome: "local" }))).toBe("update.outcome_local");
});

test("needsComposeEdit seulement sous Compose et pour un tag", () => {
  const compose = { compose_service: "web", compose_dir: "/srv/app" };
  expect(needsComposeEdit(compose, check({ kind: "minor", newer_tag: "3.22" }))).toBe(true);
  expect(needsComposeEdit(compose, check({ kind: "digest" }))).toBe(false);
  expect(needsComposeEdit({ compose_service: "", compose_dir: "" }, check({ kind: "minor", newer_tag: "3.22" }))).toBe(false);
});

test("imageLineChange donne la ligne image avant et après, sous Compose seulement", () => {
  const compose = { compose_service: "server", compose_dir: "/srv/vaultwarden", image: "vaultwarden/server:1.32.0" };
  const patch = check({ kind: "patch", newer_tag: "1.32.3", target: "vaultwarden/server:1.32.3" });
  expect(imageLineChange(compose, patch)).toEqual({ before: "image: vaultwarden/server:1.32.0", after: "image: vaultwarden/server:1.32.3" });
  expect(imageLineChange(compose, check({ kind: "digest", target: "vaultwarden/server:1.32.0" }))).toBeNull();
  expect(imageLineChange({ ...compose, compose_service: "", compose_dir: "" }, patch)).toBeNull();
});

test("shortDigest coupe comme Docker", () => {
  expect(shortDigest("sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc")).toBe("d9e853e87e55");
});

const declared: ContainerDirectory = {
  declared: true, service_id: "svc_1", service_name: "nextcloud", unit_id: "unt_1", unit_name: "db", match_level: "compose",
  weak: false, key_kind: "compose_project", key_value: "nextcloud", ignored: false,
  label_service: "", label_unit: "", label_problem: "", label_machine_name: "",
};
const minor = check({ kind: "minor", newer_tag: "16.5", target: "postgres:16.5" });
const deployed = { deployed: true };

test("upgradeOffer propose le tag plus récent d'un service déployé, suivi et déclaré", () => {
  const container = { update_policy: "watch" as const, image_check: minor };
  expect(upgradeServiceID(container, declared)).toBe("svc_1");
  expect(upgradeOffer(container, declared, deployed)).toEqual({ version: "16.5" });
});

test("upgradeOffer se tait quand une des quatre conditions manque", () => {
  const container = { update_policy: "watch" as const, image_check: minor };
  // Pas déclaré : la ligne à copier reste.
  expect(upgradeOffer(container, { ...declared, declared: false }, deployed)).toBeNull();
  expect(upgradeOffer(container, null, deployed)).toBeNull();
  // Déclaré mais jamais déployé par openCloud, ou plan pas encore lu.
  expect(upgradeOffer(container, declared, { deployed: false })).toBeNull();
  expect(upgradeOffer(container, declared, null)).toBeNull();
  // Épinglé ou exclu : la mise à jour ne se montre pas.
  expect(upgradeOffer({ ...container, update_policy: "hold" }, declared, deployed)).toBeNull();
  expect(upgradeOffer({ ...container, update_policy: "ignore" }, declared, deployed)).toBeNull();
  // Rien de plus récent, ou une image reconstruite sous le même tag.
  expect(upgradeOffer({ ...container, image_check: check({}) }, declared, deployed)).toBeNull();
  expect(upgradeOffer({ ...container, image_check: check({ kind: "digest" }) }, declared, deployed)).toBeNull();
  expect(upgradeOffer({ ...container, image_check: null }, declared, deployed)).toBeNull();
});

test("upgradeServiceID ne demande pas le plan quand rien n'est offert", () => {
  expect(upgradeServiceID({ update_policy: "hold", image_check: minor }, declared)).toBeNull();
  expect(upgradeServiceID({ update_policy: "watch", image_check: check({ kind: "digest" }) }, declared)).toBeNull();
  expect(upgradeServiceID({ update_policy: "watch", image_check: minor }, null)).toBeNull();
});
