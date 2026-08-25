import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from "react";
import type { ReactNode } from "react";
import type {
  AdministratorActivationTokenResponse,
  BootstrapState,
  LoginResponse,
  MfaChallengeResponse,
  RecoveryCodes,
  SessionResponse,
} from "../api/generated/control";
import { AuthApiError, generatedAuthApi } from "../api/auth-api";
import type { AuthApi } from "../api/auth-api";

export type AuthRoute = "loading" | "bootstrap" | "login" | "activation" | "management" | "one-time" | "unavailable";

export interface OneTimeMaterial {
  kind: "recovery_codes" | "activation_token";
  values: string[];
  expiresAt?: string;
}

interface AuthState {
  route: AuthRoute;
  bootstrapState?: BootstrapState;
  session: SessionResponse | null;
  challenge: MfaChallengeResponse | null;
  oneTime: OneTimeMaterial | null;
  api: AuthApi;
  navigate(route: AuthRoute): void;
  setChallenge(challenge: MfaChallengeResponse | null): void;
  acceptLogin(response: LoginResponse): void;
  acceptSession(session: SessionResponse): void;
  showRecoveryCodes(codes: RecoveryCodes): void;
  showActivationToken(response: AdministratorActivationTokenResponse): void;
  discardOneTime(): void;
  leaveOneTime(): void;
  refreshSession(): Promise<SessionResponse | null>;
  clearSession(): void;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children, api = generatedAuthApi }: { children: ReactNode; api?: AuthApi }) {
  const [route, setRoute] = useState<AuthRoute>("loading");
  const [bootstrapState, setBootstrapState] = useState<BootstrapState>();
  const [session, setSession] = useState<SessionResponse | null>(null);
  const [challenge, setChallenge] = useState<MfaChallengeResponse | null>(null);
  const [oneTime, setOneTime] = useState<OneTimeMaterial | null>(null);
  const oneTimeRef = useRef<OneTimeMaterial | null>(null);
  const sessionRefreshRef = useRef<Promise<SessionResponse | null> | null>(null);

  const wipeOneTime = useCallback(() => {
    const material = oneTimeRef.current;
    if (material) material.values.fill("");
    oneTimeRef.current = null;
    setOneTime(null);
  }, []);

  const clearSession = useCallback(() => {
    setSession(null);
    setChallenge(null);
    wipeOneTime();
    setRoute("login");
  }, [wipeOneTime]);

  const refreshSession = useCallback((): Promise<SessionResponse | null> => {
    if (sessionRefreshRef.current) return sessionRefreshRef.current;

    const request = api.session().then((current) => {
      setSession(current);
      if (current) setRoute("management");
      else setRoute("login");
      return current;
    });
    sessionRefreshRef.current = request;
    const clear = () => {
      if (sessionRefreshRef.current === request) sessionRefreshRef.current = null;
    };
    void request.then(clear, clear);
    return request;
  }, [api]);

  useEffect(() => {
    let active = true;
    void (async () => {
      try {
        const status = await api.bootstrapStatus();
        if (!active) return;
        setBootstrapState(status);
        if (status !== "completed") {
          setRoute("bootstrap");
          return;
        }
        const current = await api.session();
        if (!active) return;
        setSession(current);
        setRoute(current ? "management" : "login");
      } catch {
        if (active) setRoute("unavailable");
      }
    })();
    return () => {
      active = false;
    };
  }, [api]);

  const navigate = useCallback((next: AuthRoute) => {
    wipeOneTime();
    setRoute(next);
  }, [wipeOneTime]);

  const acceptSession = useCallback((next: SessionResponse) => {
    setSession(next);
    setChallenge(null);
    setRoute("management");
  }, []);

  const acceptLogin = useCallback((response: LoginResponse) => {
    if ("state" in response && response.state === "mfa_required") {
      setChallenge(response);
      return;
    }
    acceptSession(response as SessionResponse);
  }, [acceptSession]);

  const showRecoveryCodes = useCallback((codes: RecoveryCodes) => {
    wipeOneTime();
    const material: OneTimeMaterial = { kind: "recovery_codes", values: [...codes] };
    oneTimeRef.current = material;
    setOneTime(material);
    setRoute("one-time");
  }, [wipeOneTime]);

  const showActivationToken = useCallback((response: AdministratorActivationTokenResponse) => {
    wipeOneTime();
    const material: OneTimeMaterial = { kind: "activation_token", values: [response.activation_token], expiresAt: response.expires_at };
    oneTimeRef.current = material;
    setOneTime(material);
    setRoute("one-time");
  }, [wipeOneTime]);

  const discardOneTime = useCallback(() => {
    wipeOneTime();
    setRoute(session ? "management" : "login");
  }, [session, wipeOneTime]);

  const leaveOneTime = useCallback(() => {
    discardOneTime();
  }, [discardOneTime]);

  const value = useMemo<AuthState>(() => ({
    route,
    bootstrapState,
    session,
    challenge,
    oneTime,
    api,
    navigate,
    setChallenge,
    acceptLogin,
    acceptSession,
    showRecoveryCodes,
    showActivationToken,
    discardOneTime,
    leaveOneTime,
    refreshSession,
    clearSession,
  }), [route, bootstrapState, session, challenge, oneTime, api, navigate, acceptLogin, acceptSession, showRecoveryCodes, showActivationToken, discardOneTime, leaveOneTime, refreshSession, clearSession]);

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthState {
  const value = useContext(AuthContext);
  if (!value) throw new Error("useAuth must be used inside AuthProvider");
  return value;
}

export function handleSessionError(error: unknown, clearSession: () => void): void {
  if (error instanceof AuthApiError && error.status === 401) clearSession();
}
