import { describe, expect, it } from "vitest";

import { installPlan, missingSoftware, resticInstall, resticReleasePage, restServerInstall, restServerReleasePage } from "./resticInstall";

describe("installer rest-server", () => {
  it("donne les commandes de l'architecture de la machine, sans rien à remplacer", () => {
    const plan = restServerInstall("arm64");
    expect(plan.page).toBeNull();
    expect(plan.steps.map((step) => step.command)).toEqual([
      "curl -fsSLO https://github.com/restic/rest-server/releases/download/v0.14.0/rest-server_0.14.0_linux_arm64.tar.gz",
      "curl -fsSLO https://github.com/restic/rest-server/releases/download/v0.14.0/SHA256SUMS",
      "sha256sum -c --ignore-missing SHA256SUMS",
      "tar -xzf rest-server_0.14.0_linux_arm64.tar.gz",
      "sudo install -m 0755 rest-server_0.14.0_linux_arm64/rest-server /usr/local/bin/rest-server",
    ]);
    expect(restServerInstall("amd64").steps[0]?.command).toContain("rest-server_0.14.0_linux_amd64.tar.gz");
    for (const step of plan.steps) {
      expect(step.command).not.toMatch(/[<>{}]/);
    }
  });

  it("n'invente pas de commande pour une autre architecture : la page des versions", () => {
    expect(restServerInstall("riscv64")).toEqual({ steps: [], page: restServerReleasePage });
    expect(restServerInstall("")).toEqual({ steps: [], page: restServerReleasePage });
    expect(restServerReleasePage).toBe("https://github.com/restic/rest-server/releases/tag/v0.14.0");
  });
});

describe("installer restic", () => {
  it("passe par les paquets sur Debian, Ubuntu et Raspbian", () => {
    for (const os of ["Debian 12", "Ubuntu 24.04 LTS", "Raspbian GNU/Linux 12", "debian"]) {
      expect(resticInstall(os).steps.map((step) => step.command)).toEqual(["sudo apt install restic"]);
    }
  });

  it("mène à la page des versions ailleurs", () => {
    expect(resticInstall("Rocky Linux 9")).toEqual({ steps: [], page: resticReleasePage });
    expect(resticInstall("")).toEqual({ steps: [], page: resticReleasePage });
  });

  it("choisit selon ce qui manque", () => {
    expect(installPlan("restic", { os: "Debian 12", arch: "riscv64" }).steps).toHaveLength(1);
    expect(installPlan("rest-server", { os: "Debian 12", arch: "riscv64" }).page).toBe(restServerReleasePage);
  });
});

describe("un refus qui dit un binaire manquant", () => {
  it("mène au même bloc, qu'il vienne du serveur ou de l'agent", () => {
    expect(missingSoftware("restic.restic_missing")).toBe("restic");
    expect(missingSoftware("restic_missing")).toBe("restic");
    expect(missingSoftware("restic.rest_server_missing")).toBe("rest-server");
    expect(missingSoftware("rest_server_missing")).toBe("rest-server");
    expect(missingSoftware("restic.retention_below_floor")).toBeNull();
  });
});
