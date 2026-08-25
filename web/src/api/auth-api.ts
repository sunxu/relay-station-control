import {
  changePassword,
  completeAdministratorActivation,
  completeBootstrap,
  completeLoginMfa,
  createAdministrator,
  disableAdministrator,
  getBootstrapStatus,
  getSession,
  listAdministrators,
  login,
  logout,
  reauthenticate,
  regenerateAdministratorActivationToken,
  regenerateRecoveryCodes,
  resetAdministratorMfa,
  resetPendingBootstrap,
  startBootstrap,
} from "./generated/control";
import type {
  Administrator,
  AdministratorActivationRequest,
  AdministratorActivationResponse,
  AdministratorActivationTokenResponse,
  AdministratorListResponse,
  BootstrapCompleteResponse,
  BootstrapStartRequest,
  BootstrapStartResponse,
  BootstrapState,
  ChangePasswordRequest,
  CreateAdministratorRequest,
  ErrorResponse,
  LoginRequest,
  LoginResponse,
  MfaChallengeRequest,
  ReasonRequest,
  ReauthenticateRequest,
  RecoveryCodesResponse,
  SessionResponse,
} from "./generated/control";

type GeneratedResponse<T> = {
  data: T | ErrorResponse;
  status: number;
  headers: Headers;
};

export class AuthApiError extends Error {
  readonly status: number;
  readonly detail: ErrorResponse;

  constructor(status: number, detail: ErrorResponse) {
    super(detail.message);
    this.name = "AuthApiError";
    this.status = status;
    this.detail = detail;
  }
}

const isErrorResponse = (value: unknown): value is ErrorResponse =>
  typeof value === "object" && value !== null && "code" in value && "request_id" in value;

function unwrap<T>(response: GeneratedResponse<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  const fallback: ErrorResponse = {
    code: "internal_error",
    message: "请求未完成",
    request_id: "client-unknown",
  };
  throw new AuthApiError(response.status, isErrorResponse(response.data) ? response.data : fallback);
}

const options = (headers?: Record<string, string>): RequestInit => ({
  cache: "no-store",
  credentials: "same-origin",
  headers,
});

export interface AuthApi {
  bootstrapStatus(): Promise<BootstrapState>;
  bootstrapStart(secret: string, input: BootstrapStartRequest): Promise<BootstrapStartResponse>;
  bootstrapComplete(secret: string, code: string): Promise<BootstrapCompleteResponse>;
  bootstrapReset(secret: string): Promise<BootstrapState>;
  session(): Promise<SessionResponse | null>;
  login(input: LoginRequest): Promise<LoginResponse>;
  completeMfa(input: MfaChallengeRequest): Promise<SessionResponse>;
  logout(csrfToken?: string): Promise<void>;
  activate(input: AdministratorActivationRequest): Promise<AdministratorActivationResponse>;
  reauthenticate(csrfToken: string, input: ReauthenticateRequest): Promise<SessionResponse>;
  changePassword(csrfToken: string, input: ChangePasswordRequest): Promise<SessionResponse>;
  recoveryCodes(csrfToken: string, input: ReasonRequest): Promise<RecoveryCodesResponse>;
  administrators(): Promise<AdministratorListResponse>;
  createAdministrator(csrfToken: string, input: CreateAdministratorRequest): Promise<AdministratorActivationTokenResponse>;
  disableAdministrator(csrfToken: string, id: string, input: ReasonRequest): Promise<Administrator>;
  activationToken(csrfToken: string, id: string, input: ReasonRequest): Promise<AdministratorActivationTokenResponse>;
  resetMfa(csrfToken: string, id: string, input: ReasonRequest): Promise<AdministratorActivationTokenResponse>;
}

export const generatedAuthApi: AuthApi = {
  async bootstrapStatus() {
    return unwrap(await getBootstrapStatus(options())).status;
  },
  async bootstrapStart(secret, input) {
    return unwrap(await startBootstrap(input, options({ "X-Bootstrap-Secret": secret })));
  },
  async bootstrapComplete(secret, code) {
    return unwrap(await completeBootstrap({ totp_code: code }, options({ "X-Bootstrap-Secret": secret })));
  },
  async bootstrapReset(secret) {
    return unwrap(await resetPendingBootstrap(options({ "X-Bootstrap-Secret": secret }))).status;
  },
  async session() {
    const response = await getSession(options());
    if (response.status === 401) return null;
    return unwrap(response);
  },
  async login(input) {
    return unwrap(await login(input, options()));
  },
  async completeMfa(input) {
    return unwrap(await completeLoginMfa(input, options()));
  },
  async logout(csrfToken) {
    unwrap<void>(await logout(options(csrfToken ? { "X-CSRF-Token": csrfToken } : undefined)));
  },
  async activate(input) {
    return unwrap(await completeAdministratorActivation(input, options()));
  },
  async reauthenticate(csrfToken, input) {
    return unwrap(await reauthenticate(input, options({ "X-CSRF-Token": csrfToken })));
  },
  async changePassword(csrfToken, input) {
    return unwrap(await changePassword(input, options({ "X-CSRF-Token": csrfToken })));
  },
  async recoveryCodes(csrfToken, input) {
    return unwrap(await regenerateRecoveryCodes(input, options({ "X-CSRF-Token": csrfToken })));
  },
  async administrators() {
    return unwrap(await listAdministrators({ limit: 100 }, options()));
  },
  async createAdministrator(csrfToken, input) {
    return unwrap(await createAdministrator(input, options({ "X-CSRF-Token": csrfToken })));
  },
  async disableAdministrator(csrfToken, id, input) {
    return unwrap(await disableAdministrator(id, input, options({ "X-CSRF-Token": csrfToken })));
  },
  async activationToken(csrfToken, id, input) {
    return unwrap(await regenerateAdministratorActivationToken(id, input, options({ "X-CSRF-Token": csrfToken })));
  },
  async resetMfa(csrfToken, id, input) {
    return unwrap(await resetAdministratorMfa(id, input, options({ "X-CSRF-Token": csrfToken })));
  },
};

export function userFacingError(error: unknown): string {
  if (!(error instanceof AuthApiError)) return "请求暂时无法完成，请稍后再试。";
  if (error.detail.code === "rate_limited") return "尝试次数过多，请稍后再试。";
  if (error.detail.code === "challenge_expired") return "验证已过期，请重新登录。";
  if (error.detail.code === "last_administrator_protected") return "不能禁用最后一个可用管理员。";
  if (error.detail.code === "administrator_self_disable_forbidden") return "不能禁用当前登录账号。";
  if (error.detail.code === "reauthentication_required") return "请先完成重新认证。";
  if (error.status === 401) return "认证失败，请检查输入后重试。";
  if (error.detail.code === "csrf_invalid") return "安全凭据已变化，请刷新会话后重试。";
  return `请求未完成（请求 ID：${error.detail.request_id}）`;
}
