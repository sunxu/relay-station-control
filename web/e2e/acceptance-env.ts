export function requireAcceptanceEnv(context: string, names: readonly string[]): void {
  const missing = names.filter((name) => !process.env[name]);
  if (missing.length > 0) {
    throw new Error(`MISSING_REQUIRED_ACCEPTANCE_ENV context=${context} variables=${missing.join(",")}`);
  }
}
