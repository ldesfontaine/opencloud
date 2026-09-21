import { describe, expect, it } from "vitest";

import type { Certificate, CertificateThresholds } from "../api/types";
import { daysUntil, read, soonest } from "./certificates";

const now = Date.parse("2026-09-20T12:00:00Z");
const thresholds: CertificateThresholds = { warning: 30, danger: 7 };

function certificate(notAfter: string, extra: Partial<Certificate> = {}): Certificate {
  return {
    subject: "cloud.exemple.fr",
    issuer: "Autorité de test",
    not_before: "2026-06-22T12:00:00Z",
    not_after: notAfter,
    fingerprint: "ab",
    chain_valid: true,
    hostname_match: true,
    ocsp: "",
    ...extra,
  };
}

function inDays(days: number): string {
  return new Date(now + days * 24 * 60 * 60 * 1000).toISOString();
}

describe("daysUntil", () => {
  it("arrondit vers le haut ce qui reste : douze heures font encore un jour", () => {
    expect(daysUntil(new Date(now + 12 * 60 * 60 * 1000).toISOString(), now)).toBe(1);
  });

  it("arrondit vers le bas ce qui est passé, pour ne jamais confondre les deux", () => {
    expect(daysUntil(new Date(now - 12 * 60 * 60 * 1000).toISOString(), now)).toBe(-1);
  });
});

describe("read", () => {
  it("laisse tranquille un certificat qui a du temps devant lui", () => {
    const reading = read(certificate(inDays(84)), thresholds, now);
    expect(reading.state).toBe("valid");
    expect(reading.tone).toBe("ok");
    expect(reading.days).toBe(84);
  });

  it("passe à renouveler au seuil, sans attendre un jour de plus", () => {
    expect(read(certificate(inDays(30)), thresholds, now).state).toBe("to_renew");
    expect(read(certificate(inDays(31)), thresholds, now).state).toBe("valid");
  });

  it("passe au danger sous le second seuil", () => {
    expect(read(certificate(inDays(12)), thresholds, now).tone).toBe("warn");
    expect(read(certificate(inDays(7)), thresholds, now).tone).toBe("danger");
  });

  it("dit expiré une fois l'échéance passée", () => {
    const reading = read(certificate(inDays(-1)), thresholds, now);
    expect(reading.state).toBe("expired");
    expect(reading.tone).toBe("danger");
    expect(reading.days).toBe(-1);
  });

  // Le cœur de la décision : l'échéance et la confiance sont deux faits.
  // Le doute se dit par sa propre pastille, il ne teinte pas la date.
  it("garde l'échéance lisible et franche sur une chaîne qu'on ne peut pas vérifier", () => {
    const reading = read(certificate(inDays(84), { chain_valid: false }), thresholds, now);
    expect(reading.untrusted).toBe(true);
    expect(reading.days).toBe(84);
    expect(reading.state).toBe("valid");
    expect(reading.tone).toBe("ok");
  });

  it("compte un nom qui ne correspond pas comme une chaîne douteuse", () => {
    expect(read(certificate(inDays(84), { hostname_match: false }), thresholds, now).untrusted).toBe(true);
  });

  it("ne retient de l'agrafe que la révocation ; le reste ne conclut rien", () => {
    expect(read(certificate(inDays(84), { ocsp: "revoked" }), thresholds, now).revoked).toBe(true);
    expect(read(certificate(inDays(84), { ocsp: "unknown" }), thresholds, now).revoked).toBe(false);
    expect(read(certificate(inDays(84), { ocsp: "" }), thresholds, now).revoked).toBe(false);
  });

  it("colore sur la seule échéance, doute ou pas", () => {
    expect(read(certificate(inDays(3), { chain_valid: false }), thresholds, now).tone).toBe("danger");
    expect(read(certificate(inDays(3), { chain_valid: true }), thresholds, now).tone).toBe("danger");
  });
});

describe("soonest", () => {
  it("rend le plus pressé, en ignorant les sondes qui n'ont rien vu", () => {
    const closest = soonest([null, certificate(inDays(84)), certificate(inDays(12)), null]);
    expect(closest?.not_after).toBe(inDays(12));
  });

  it("rend rien quand aucune sonde n'a vu de certificat", () => {
    expect(soonest([null, null])).toBeNull();
  });
});

describe("read, sur un certificat expiré", () => {
  // Une chaîne refusée parce que le certificat est expiré n'apprend rien
  // de plus : « Expiré » le dit déjà, et « Non vérifié » à côté ferait
  // croire à un second problème.
  it("ne compte pas comme douteuse une chaîne que la seule expiration a fait refuser", () => {
    const reading = read(certificate(inDays(-3), { chain_valid: false }), thresholds, now);
    expect(reading.state).toBe("expired");
    expect(reading.untrusted).toBe(false);
  });

  it("garde le nom qui ne correspond pas, lui, même passée l'échéance", () => {
    expect(read(certificate(inDays(-3), { chain_valid: false, hostname_match: false }), thresholds, now).untrusted).toBe(true);
  });
});
