import { AppLocale } from "./locale";

const dateTimeOptions: Intl.DateTimeFormatOptions = {
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
  hour: "2-digit",
  minute: "2-digit",
  second: "2-digit",
  hour12: false,
};

export function formatDateTime(value: string | null | undefined, locale: AppLocale): string {
  if (!value) return "—";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "—";
  return new Intl.DateTimeFormat(locale, dateTimeOptions).format(date);
}

export function formatNumber(value: number | null | undefined, locale: AppLocale): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—";
  return new Intl.NumberFormat(locale).format(value);
}

export function formatPercent(value: number | null | undefined, locale: AppLocale): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—";
  return new Intl.NumberFormat(locale, { style: "percent", maximumFractionDigits: 2 }).format(value);
}
