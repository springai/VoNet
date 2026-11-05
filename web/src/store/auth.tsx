import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from "react";
import * as api from "../api";
import { isStaleAuthRequest, loginIdentityFromResponse, type AuthUser } from "./authModel";

interface AuthContextValue {
  ready: boolean;
  isAuthenticated: boolean;
  user: AuthUser | null;
  login: (username: string, password: string) => Promise<boolean>;
  logout: () => Promise<void>;
  refresh: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthUser | null>(null);
  const [ready, setReady] = useState(false);
  const loginCompletedAt = useRef(0);
  const authGeneration = useRef(0);

  const refresh = useCallback(async () => {
    const generation = ++authGeneration.current;
    try {
      const current = await api.session();
      if (generation !== authGeneration.current) return;
      setUser(current.authenticated ? { username: current.username, role: current.role } : null);
    } catch {
      if (generation !== authGeneration.current) return;
      setUser(null);
    } finally {
      if (generation === authGeneration.current) setReady(true);
    }
  }, []);

  const login = useCallback(
    async (username: string, password: string) => {
      try {
        const result = await api.login(username, password);
        authGeneration.current += 1;
        loginCompletedAt.current = Date.now();
        setUser(loginIdentityFromResponse(result, username));
        setReady(true);
        return true;
      } catch {
        return false;
      }
    },
    [],
  );

  const logout = useCallback(async () => {
    authGeneration.current += 1;
    loginCompletedAt.current = 0;
    try {
      await api.logout();
    } finally {
      setUser(null);
    }
  }, []);

  useEffect(() => {
    const unauthorized = (event: Event) => {
      const detail = (event as CustomEvent<{ requestStartedAt?: number }>).detail;
      if (isStaleAuthRequest(detail?.requestStartedAt, loginCompletedAt.current)) return;
      authGeneration.current += 1;
      setUser(null);
      setReady(true);
    };
    window.addEventListener("vonet:unauthorized", unauthorized);
    return () => window.removeEventListener("vonet:unauthorized", unauthorized);
  }, []);

  useEffect(() => {
    void refresh();
  }, [refresh]);

  const value = useMemo<AuthContextValue>(
    () => ({ ready, isAuthenticated: !!user, user, login, logout, refresh }),
    [ready, user, login, logout, refresh],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth() {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth must be used within AuthProvider");
  return ctx;
}
