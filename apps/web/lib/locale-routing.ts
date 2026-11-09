import {
  matchLocale,
  SUPPORTED_LOCALES,
  type SupportedLocale,
} from "@multica/core/i18n";

export const MULTICA_LOCALE_HEADER = "x-multica-locale";

export function isSupportedLocale(
  value: string | null,
): value is SupportedLocale {
  return (
    value !== null &&
    (SUPPORTED_LOCALES as readonly string[]).includes(value)
  );
}

export function resolveLocaleFromSignals({
  cookieLocale,
  acceptLanguage,
}: {
  cookieLocale?: string | null;
  acceptLanguage?: string | null;
}): SupportedLocale {
  const candidates: string[] = [];
  if (cookieLocale) candidates.push(cookieLocale);

  for (const part of (acceptLanguage ?? "").split(",")) {
    const tag = part.split(";")[0]?.trim();
    if (tag) candidates.push(tag);
  }

  // yanqu-default-zh: 未显式选择语言时默认简体中文
  if (!cookieLocale) return "zh-Hans";
  return matchLocale(candidates);
}
