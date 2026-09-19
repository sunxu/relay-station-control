const canonicalTranslation = {
  common: {
    locale: {
      label: "语言",
    },
    readState: {
      loading: "加载中",
      unavailable: "暂不可用",
      empty: "暂无记录",
      retry: "重试",
    },
  },
} as const;

type LeafKeys<T> = {
  [Key in keyof T & string]: T[Key] extends Record<string, unknown>
    ? `${Key}.${LeafKeys<T[Key]>}`
    : Key;
}[keyof T & string];

type ResourceShape<T> = {
  [Key in keyof T]: T[Key] extends Record<string, unknown> ? ResourceShape<T[Key]> : string;
};

export type TranslationKey = LeafKeys<typeof canonicalTranslation>;
export type TranslationResource = ResourceShape<typeof canonicalTranslation>;

export const resources = {
  "zh-CN": {
    translation: canonicalTranslation,
  },
  en: {
    translation: {
      common: {
        locale: {
          label: "Language",
        },
        readState: {
          loading: "Loading",
          unavailable: "Unavailable",
          empty: "No records",
          retry: "Retry",
        },
      },
    } satisfies TranslationResource,
  },
} satisfies Record<"zh-CN" | "en", { translation: TranslationResource }>;
