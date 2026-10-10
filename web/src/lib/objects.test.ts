import { expect, test } from "vitest";

import { nameWithMachine } from "./objects";

test("nameWithMachine distingue deux objets de même nom par leur machine", () => {
  const onWeb = nameWithMachine({ name: "nginx", machine_name: "web-1" });
  const onEdge = nameWithMachine({ name: "nginx", machine_name: "edge" });
  expect(onWeb).toBe("nginx · web-1");
  expect(onWeb).not.toBe(onEdge);
});

test("nameWithMachine laisse le nom seul quand rien n'est rattaché à une machine", () => {
  expect(nameWithMachine({ name: "sauvegarde nextcloud", machine_name: "" })).toBe("sauvegarde nextcloud");
});
