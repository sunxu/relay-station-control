import type {
  ErrorResponse,
  ProblemAccountIssue,
  ProblemAccountItem,
  ProblemAccountQueryRequest,
  ProblemAccountResponse,
} from "./generated/control";

export type {
  ProblemAccountIssue,
  ProblemAccountItem,
  ProblemAccountQueryRequest,
  ProblemAccountResponse,
};

export interface ProblemAccountsApi {
  query(
    request: ProblemAccountQueryRequest,
    csrfToken: string,
    signal?: AbortSignal,
  ): Promise<ProblemAccountResponse>;
}

export class ProblemAccountsApiError extends Error {
  readonly status: number;
  readonly detail: ErrorResponse;

  constructor(status: number, detail: ErrorResponse) {
    super("problem accounts request failed");
    this.name = "ProblemAccountsApiError";
    this.status = status;
    this.detail = detail;
  }
}
