import { expect, test } from "vitest";

import type { Disk } from "../api/resources";
import { diskSettingsError, fullestFirst } from "./disks";

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

const bounds = { min_attention: 50, max_danger: 99 };

test("diskSettingsError accepte 50 ≤ attention < danger ≤ 99", () => {
  expect(diskSettingsError({ attention: 85, danger: 95 }, bounds)).toBeNull();
  expect(diskSettingsError({ attention: 50, danger: 99 }, bounds)).toBeNull();
});

test("diskSettingsError refuse un seuil hors bornes, inversé, égal ou non entier", () => {
  for (const thresholds of [
    { attention: 49, danger: 95 },
    { attention: 85, danger: 100 },
    { attention: 90, danger: 90 },
    { attention: 95, danger: 85 },
    { attention: 85.5, danger: 95 },
    { attention: Number(""), danger: 95 },
  ]) {
    expect(diskSettingsError(thresholds, bounds)).toBe("settings.disk_invalid");
  }
});
