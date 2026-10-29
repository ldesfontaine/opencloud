import { renderToStaticMarkup } from "react-dom/server";
import { MemoryRouter } from "react-router";
import { expect, test, vi } from "vitest";

import type { Machine } from "../api/machines";
import { ProxmoxCard } from "./MachineHypervisor";

// Sans serveur, la traduction rend la clé et ses arguments : le test lit ce que la page demande.
vi.mock("../i18n/context", () => ({
  useT: () => (key: string, ...args: (string | number)[]) => (args.length === 0 ? key : `${key}(${args.join("|")})`),
  useI18n: () => ({ language: "fr" }),
}));

function machine(over: Partial<Machine> = {}): Machine {
  return {
    id: "m1",
    name: "pve",
    hostname: "pve",
    address: "192.0.2.10",
    os: "Debian 12",
    arch: "amd64",
    online: true,
    update_last: false,
    last_seen_at: null,
    agent: null,
    proxmox: { declared: false, guests: 0, last_error: "", hosted: null },
    roles: [],
    ...over,
  };
}

function render(current: Machine): string {
  return renderToStaticMarkup(
    <MemoryRouter>
      <ProxmoxCard machine={current} />
    </MemoryRouter>,
  );
}

test("un hôte Proxmox reconnu mais non déclaré propose de le déclarer, et dit quel jeton créer", () => {
  const markup = render(machine({ roles: [{ name: "proxmox_ve", version: "8.2.4" }] }));
  expect(markup).toContain("machine.proxmox_detected(8.2.4)");
  expect(markup).toContain("machine.proxmox_detected_token");
  expect(markup).toContain('href="/machines/m1/proxmox"');
  expect(markup).not.toContain("machine.proxmox_declare_hint");
});

test("sans version lisible, l'encadré ne montre pas de version", () => {
  const markup = render(machine({ roles: [{ name: "proxmox_ve", version: "" }] }));
  expect(markup).toContain("machine.proxmox_detected_unversioned");
  expect(markup).not.toContain("machine.proxmox_detected(");
});

test("une machine sans rôle garde l'encart d'avant", () => {
  const markup = render(machine());
  expect(markup).toContain("machine.proxmox_declare_hint");
  expect(markup).not.toContain("machine.proxmox_detected");
});

test("un hôte reconnu et déjà déclaré ne propose plus rien : l'état du relevé reste seul", () => {
  const markup = render(machine({ roles: [{ name: "proxmox_ve", version: "8.2.4" }], proxmox: { declared: true, guests: 2, last_error: "", hosted: null } }));
  expect(markup).not.toContain("machine.proxmox_detected");
  expect(markup).toContain("machine.proxmox_open");
});
