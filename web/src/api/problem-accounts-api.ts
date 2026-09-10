import { queryProblemAccounts } from "./generated/control";
import type {
  ErrorResponse,
  ProblemAccountQueryRequest,
  ProblemAccountResponse,
} from "./generated/control";
import { ProblemAccountsApiError } from "./problem-accounts-types";
import type { ProblemAccountsApi } from "./problem-accounts-types";

type GeneratedResponse<T> = { data: T | unknown; status: number };

function isErrorResponse(value: unknown): value is ErrorResponse {
  return typeof value === "object" && value !== null && "code" in value && "request_id" in value;
}

function normalizeRequest(request: ProblemAccountQueryRequest): ProblemAccountQueryRequest {
  const normalized: ProblemAccountQueryRequest = { ...request };
  const provider = request.provider?.trim();
  const node = request.node?.trim();
  const email = request.email?.trim().toLowerCase();

  if (provider) normalized.provider = provider;
  else delete normalized.provider;
  if (node) normalized.node = node;
  else delete normalized.node;
  if (email) normalized.email = email;
  else delete normalized.email;
  if (!request.severity) delete normalized.severity;
  if (!request.reason) delete normalized.reason;
  if (!request.cursor) delete normalized.cursor;
  if (request.limit === undefined) delete normalized.limit;
  return normalized;
}

function unwrap<T>(response: GeneratedResponse<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  const fallback: ErrorResponse = { code: "internal_error", message: "请求未完成", request_id: "client-unknown" };
  throw new ProblemAccountsApiError(response.status, isErrorResponse(response.data) ? response.data : fallback);
}

export const generatedProblemAccountsApi: ProblemAccountsApi = {
  async query(request, csrfToken, signal) {
    const response = await queryProblemAccounts(normalizeRequest(request), {
      signal,
      cache: "no-store",
      credentials: "same-origin",
      headers: { "X-CSRF-Token": csrfToken },
    });
    return unwrap<ProblemAccountResponse>(response);
  },
};
