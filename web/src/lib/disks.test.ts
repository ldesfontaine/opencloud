import { expect, test } from "vitest";

import type { Disk } from "../api/types";
import { fullestFirst } from "./disks";

const gib = 1024 ** 3;
const root: Disk = { mount_point: "/", device: "/dev/vda1", used: 41 * gib, total: 80 * gib };
const data: Disk = { mount_point: "/data", device: "/dev/vdb", used: 300 * gib, total: 500 * gib };
const backups: Disk = { mount_point: "/backups", device: "/dev/vdc", used: 30 * gib, total: 50 * gib };

test("fullestFirst met le volume le plus plein en tête, puis trie par point de montage", () => {
  expect(fullestFirst([root, data, backups]).map((disk) => disk.mount_point)).toEqual(["/backups", "/data", "/"]);
});

test("fullestFirst ne touche pas à la liste reçue", () => {
  const disks = [root, data];
  fullestFirst(disks);
  expect(disks[0]).toBe(root);
});
