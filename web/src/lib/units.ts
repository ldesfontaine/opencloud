import type { Translate } from "../i18n/context";

// Les octets se lisent en puissances de 1024, comme df et htop : un disque
// de 80 Go affiché ici est celui que la machine annonce.
const step = 1024;
const unitKeys = ["unit.b", "unit.kb", "unit.mb", "unit.gb", "unit.tb"];

export interface Quantity {
  value: string;
  unit: string;
}

// Une décimale sous 10, aucune au-delà : « 3,1 Go », « 41 Go », « 768 Mo ».
function formatNumber(value: number, locale: string): string {
  const digits = value < 10 && value !== Math.floor(value) ? 1 : 0;
  return new Intl.NumberFormat(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value);
}

// Choisit l'unité qui garde le nombre sous 1024.
export function bytesQuantity(t: Translate, locale: string, bytes: number): Quantity {
  let value = Math.max(0, bytes);
  let index = 0;
  while (value >= step && index < unitKeys.length - 1) {
    value /= step;
    index += 1;
  }
  return { value: formatNumber(value, locale), unit: t(unitKeys[index] ?? "unit.b") };
}

// Comme bytesQuantity, mais dans l'unité d'une autre quantité : « 3,1 » à
// côté de « 8 Go », pour lire « 3,1 Go / 8 ».
export function bytesIn(locale: string, bytes: number, unitOf: number): string {
  let divisor = 1;
  let reference = Math.max(0, unitOf);
  while (reference >= step && divisor < step ** (unitKeys.length - 1)) {
    reference /= step;
    divisor *= step;
  }
  return formatNumber(Math.max(0, bytes) / divisor, locale);
}

export function formatBytes(t: Translate, locale: string, bytes: number): string {
  const quantity = bytesQuantity(t, locale, bytes);
  return `${quantity.value} ${quantity.unit}`;
}

// Un débit : « 1,2 Mo/s ».
export function rateQuantity(t: Translate, locale: string, bytesPerSecond: number): Quantity {
  const quantity = bytesQuantity(t, locale, bytesPerSecond);
  return { value: quantity.value, unit: t("unit.per_second", quantity.unit) };
}

export function formatRate(t: Translate, locale: string, bytesPerSecond: number): string {
  const quantity = rateQuantity(t, locale, bytesPerSecond);
  return `${quantity.value} ${quantity.unit}`;
}

// La part d'un tout, en entier ; un tout nul ne donne rien plutôt que
// l'infini.
export function percentOf(part: number, total: number): number {
  if (total <= 0) {
    return 0;
  }
  return Math.min(100, Math.round((part / total) * 100));
}

export function formatDecimal(locale: string, value: number, digits = 1): string {
  return new Intl.NumberFormat(locale, { minimumFractionDigits: digits, maximumFractionDigits: digits }).format(value);
}
