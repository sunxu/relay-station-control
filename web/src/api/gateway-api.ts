import {
  editGatewayAsset,
  getGatewayAssetById,
  getGatewayHealth,
  listGatewayAssets,
  registerGatewayAsset,
  replaceGatewayAsset,
  retireGatewayAsset,
  testGatewayConnection,
} from "./generated/control";
import type {
  EmptyObject,
  GatewayAsset,
  GatewayAssetDetailResponse,
  GatewayAssetListResponse,
  GatewayEditRequest,
  GatewayRegisterRequest,
  GatewayReplaceRequest,
  GatewayRetireRequest,
  GatewayProbeResult,
} from "./generated/control";

export class GatewayApiError extends Error {
  readonly status: number;
  readonly code?: string;

  constructor(status: number, code?: string) {
    super("gateway request failed");
    this.name = "GatewayApiError";
    this.status = status;
    this.code = code;
  }
}

type ResponseLike<T> = { data: T | { code?: string }; status: number };

function unwrap<T>(response: ResponseLike<T>): T {
  if (response.status >= 200 && response.status < 300) return response.data as T;
  const detail = response.data as { code?: string };
  throw new GatewayApiError(response.status, detail?.code);
}

const options = (csrfToken?: string): RequestInit => ({
  cache: "no-store",
  credentials: "same-origin",
  ...(csrfToken ? { headers: { "X-CSRF-Token": csrfToken } } : {}),
});

export interface GatewayAdminApi {
  list(lifecycle?: "active" | "retired" | "all", cursor?: string): Promise<GatewayAssetListResponse>;
  detail(instanceId: string): Promise<GatewayAssetDetailResponse>;
  register(input: GatewayRegisterRequest, csrfToken: string): Promise<{ asset: GatewayAsset }>;
  edit(instanceId: string, input: GatewayEditRequest, csrfToken: string): Promise<{ asset: GatewayAsset }>;
  retire(instanceId: string, input: GatewayRetireRequest, csrfToken: string): Promise<{ asset: GatewayAsset }>;
  replace(instanceId: string, input: GatewayReplaceRequest, csrfToken: string): Promise<{ old_asset: GatewayAsset; new_asset: GatewayAsset }>;
  health(instanceId: string): Promise<GatewayProbeResult>;
  connectionTest(instanceId: string, csrfToken: string): Promise<GatewayProbeResult>;
}

export const generatedGatewayAdminApi: GatewayAdminApi = {
  async list(lifecycle = "active", cursor) {
    return unwrap(await listGatewayAssets({ lifecycle, limit: 50, cursor }, options()));
  },
  async detail(instanceId) {
    return unwrap(await getGatewayAssetById(instanceId, options()));
  },
  async register(input, csrfToken) {
    return unwrap(await registerGatewayAsset(input, options(csrfToken)));
  },
  async edit(instanceId, input, csrfToken) {
    return unwrap(await editGatewayAsset(instanceId, input, options(csrfToken)));
  },
  async retire(instanceId, input, csrfToken) {
    return unwrap(await retireGatewayAsset(instanceId, input, options(csrfToken)));
  },
  async replace(instanceId, input, csrfToken) {
    return unwrap(await replaceGatewayAsset(instanceId, input, options(csrfToken)));
  },
  async health(instanceId) {
    return unwrap(await getGatewayHealth(instanceId, options()));
  },
  async connectionTest(instanceId, csrfToken) {
    return unwrap(await testGatewayConnection(instanceId, {} as EmptyObject, options(csrfToken)));
  },
};
