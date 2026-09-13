import { describe, expect, it } from "vitest";
import { validateInternalHttpEndpoint } from "./internal-http-endpoint";

describe("validateInternalHttpEndpoint", () => {
  it.each([
    "http://gateway:8080",
    "http://gateway:8317",
    "http://node:8317",
    "http://host.docker.internal:8317",
    "http://node.example:8317",
    "http://192.168.1.10:8317",
    "http://gateway:8080/management",
  ])("accepts %s", (value) => {
    expect(validateInternalHttpEndpoint(value)).toBeUndefined();
  });

  it.each([
    "https://gateway:8080",
    "ftp://gateway",
    "gateway:8080",
    "http://",
    "http://user:pass@gateway",
    "http://gateway?x=1",
    "http://gateway#fragment",
    "http://gateway:0",
    "http://gateway:65536",
    "http://gateway:99999",
    "http://gateway/%00",
    "http://gateway/%2f",
    "http://gateway/%5c",
    "http://gateway/%2e%2e/management",
    "http://gateway/\u0001",
    "http://gateway/\tmanagement",
  ])("rejects %s", (value) => {
    expect(validateInternalHttpEndpoint(value)).toBeTruthy();
  });

  it("returns no validation error for the empty value so Form required rules own emptiness", () => {
    expect(validateInternalHttpEndpoint("")).toBeUndefined();
  });

  it("rejects non-string values supplied by an unexpected form control", () => {
    expect(validateInternalHttpEndpoint(42)).toBeTruthy();
  });
});
