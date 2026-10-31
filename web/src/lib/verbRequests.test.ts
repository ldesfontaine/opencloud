import { expect, test } from "vitest";

import type { VerbRequest } from "../api/verbRequests";
import type { Translate } from "../i18n/context";
import { requestedBy, requestExpiry, requestPhrase } from "./verbRequests";

// Un faux catalogue : la clé et ses arguments, pour lire ce qui est demandé.
const t: Translate = (key, ...args) => [key, ...args].join(":");

const request: VerbRequest = {
  id: "r1",
  verb: "machine.delete",
  object: { id: "m1", name: "nas" },
  token_name: "agent",
  on_behalf_of: "alice",
  created_at: "2026-10-31T12:00:00Z",
  expires_at: "2026-10-31T12:15:00Z",
};

test("a known verb is said with the name of its object, an unknown one by its identifier", () => {
  expect(requestPhrase(t, request)).toBe("verb_request.machine_delete:nas");
  expect(requestPhrase(t, { ...request, verb: "thing.remove" })).toBe("thing.remove : nas");
});

test("the request names the token, and the person it calls for when it said so", () => {
  expect(requestedBy(t, request)).toBe("verb_request.by_for:agent:alice");
  expect(requestedBy(t, { ...request, on_behalf_of: "" })).toBe("verb_request.by:agent");
});

test("the expiry counts down, then says it is over", () => {
  const start = Date.parse("2026-10-31T12:00:00Z");
  expect(requestExpiry(t, request, start)).toBe("verb_request.expires_in:time.minutes:15");
  expect(requestExpiry(t, request, start + 14 * 60_000 + 30_000)).toBe("verb_request.expires_in:time.seconds:30");
  expect(requestExpiry(t, request, start + 15 * 60_000)).toBe("verb_request.expired_now");
});
