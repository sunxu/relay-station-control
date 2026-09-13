const INVALID_ENDPOINT_MESSAGE = "请输入有效的 http:// 地址";
const HTTP_ONLY_MESSAGE = "仅支持 http://";

export function validateInternalHttpEndpoint(value: unknown): string | undefined {
  if (value === undefined || value === null || value === "") return undefined;
  if (typeof value !== "string" || value.length < 8 || value.length > 2048) return INVALID_ENDPOINT_MESSAGE;
  if (value !== value.trim() || /[\r\n\t ?#@\u0000-\u001f\u007f]/.test(value)) return INVALID_ENDPOINT_MESSAGE;

  let endpoint: URL;
  try {
    endpoint = new URL(value);
  } catch {
    return INVALID_ENDPOINT_MESSAGE;
  }

  if (endpoint.protocol !== "http:") return HTTP_ONLY_MESSAGE;
  if (!endpoint.hostname || endpoint.username || endpoint.password || endpoint.search || endpoint.hash || endpoint.hostname.includes("%")) {
    return INVALID_ENDPOINT_MESSAGE;
  }

  const hostname = endpoint.hostname.startsWith("[") && endpoint.hostname.endsWith("]")
    ? endpoint.hostname.slice(1, -1)
    : endpoint.hostname;
  const validHostname = hostname.includes(":")
    ? /^[0-9a-f:]+$/i.test(hostname)
    : /^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)(?:\.(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?))*$/i.test(hostname);
  if (!validHostname || hostname.includes("..")) return INVALID_ENDPOINT_MESSAGE;

  if (endpoint.port !== "") {
    const port = Number(endpoint.port);
    if (!Number.isInteger(port) || port < 1 || port > 65535) return INVALID_ENDPOINT_MESSAGE;
  }

  const pathStart = value.indexOf("/", value.indexOf("://") + 3);
  const rawPath = pathStart >= 0 ? value.slice(pathStart) : "";
  if (rawPath.includes("\\")) return INVALID_ENDPOINT_MESSAGE;
  for (let index = rawPath.indexOf("%"); index >= 0; index = rawPath.indexOf("%", index + 1)) {
    const encoded = rawPath.slice(index + 1, index + 3);
    if (!/^[0-9a-f]{2}$/i.test(encoded)) return INVALID_ENDPOINT_MESSAGE;
    const octet = Number.parseInt(encoded, 16);
    if (octet <= 0x1f || octet === 0x7f || octet === 0x2f || octet === 0x5c) return INVALID_ENDPOINT_MESSAGE;
  }
  if (rawPath.split("/").some((segment) => segment.replace(/%2e/gi, ".") === "." || segment.replace(/%2e/gi, ".") === "..")) {
    return INVALID_ENDPOINT_MESSAGE;
  }

  return undefined;
}
