import { describe, expect, expectTypeOf, it } from "vitest";
import { createAppI18n } from "./i18n";
import { resources, type TranslationKey } from "./resources";

function typedTranslationProof() {
  const i18n = createAppI18n("en");
  i18n.t("common.locale.label");
  // @ts-expect-error Unknown literal keys must be rejected by the resource contract.
  i18n.t("common.locale.missing");
}

describe("translation resources", () => {
  it("keeps the two locale resource shapes equivalent", () => {
    expect(Object.keys(resources["zh-CN"].translation.common.locale)).toEqual(
      Object.keys(resources.en.translation.common.locale),
    );
  });

  it("exposes literal translation keys", () => {
    const key: TranslationKey = "common.locale.label";
    expectTypeOf(key).toEqualTypeOf<"common.locale.label">();
    expect(key).toBe("common.locale.label");
    void typedTranslationProof;
  });
});
