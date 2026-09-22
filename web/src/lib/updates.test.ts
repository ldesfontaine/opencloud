import { expect, test } from "vitest";

import type { ImageCheck } from "../api/types";
import type { Translate } from "../i18n/context";
import { checkText, needsComposeEdit, shortDigest, updateAvailable, updateLabel } from "./updates";

const t: Translate = (key, ...args) => [key, ...args].join(":");

function check(overrides: Partial<ImageCheck>): ImageCheck {
  return {
    checked_at: "2026-09-22T12:00:00Z", outcome: "ok", local_digest: "sha256:aaaa", remote_digest: "sha256:aaaa",
    newer_tag: "", newer_digest: "", kind: "", target: "", command: "", ...overrides,
  };
}

test("updateAvailable suit le constat et la politique", () => {
  const minor = check({ kind: "minor", newer_tag: "3.22", target: "alpine:3.22" });
  expect(updateAvailable({ update_policy: "", image_check: minor })).toBe(true);
  expect(updateAvailable({ update_policy: "pinned", image_check: minor })).toBe(false);
  expect(updateAvailable({ update_policy: "excluded", image_check: minor })).toBe(true);
  expect(updateAvailable({ update_policy: "", image_check: check({}) })).toBe(false);
  expect(updateAvailable({ update_policy: "", image_check: null })).toBe(false);
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

test("shortDigest coupe comme Docker", () => {
  expect(shortDigest("sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc")).toBe("d9e853e87e55");
});
