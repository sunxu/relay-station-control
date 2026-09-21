export type CredentialAction = "keep" | "set" | "clear" | undefined;

export function buildCredentialPatch(field: "management_credential" | "directory_credential", action: CredentialAction, credential?: string): Record<string, string | null> {
  if (action === "clear") return { [field]: null };
  if (action === "set") return { [field]: credential ?? "" };
  return {};
}
