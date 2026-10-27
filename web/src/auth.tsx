import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from "react";
import { Navigate, useLocation } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api, onSessionExpired, post } from "./lib/api";
import { clearStoredOrgId } from "./lib/hooks";
import type { User } from "./lib/types";
import { Spinner } from "./components/ui";

interface AuthState {
  user: User | null;
  loading: boolean;
  login: (email: string, password: string) => Promise<User>;
  register: (email: string, password: string, inviteToken?: string) => Promise<User>;
  logout: () => Promise<void>;
}

const AuthContext = createContext<AuthState | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  const [loading, setLoading] = useState(true);
  const queryClient = useQueryClient();

  // Drops everything cached for the previous session so the next user never
  // sees it, and forgets their last-active org.
  const resetSession = useCallback(() => {
    queryClient.clear();
    clearStoredOrgId();
  }, [queryClient]);

  useEffect(() => {
    api<User>("/auth/me")
      .then(setUser)
      .catch(() => setUser(null))
      .finally(() => setLoading(false));
  }, []);

  // A refresh the server refused means the session is over: drop the user so
  // RequireAuth sends them to /login instead of leaving them in a shell where
  // every query fails.
  useEffect(
    () =>
      onSessionExpired(() => {
        setUser((prev) => {
          if (prev) resetSession();
          return null;
        });
      }),
    [resetSession],
  );

  const login = useCallback(
    async (email: string, password: string) => {
      const u = await post<User>("/auth/login", { email, password });
      resetSession();
      setUser(u);
      return u;
    },
    [resetSession],
  );

  const register = useCallback(
    async (email: string, password: string, inviteToken?: string) => {
      // An invite-only instance needs the token at sign-up, not just at accept.
      const u = await post<User>("/auth/register", {
        email,
        password,
        ...(inviteToken ? { invite_token: inviteToken } : {}),
      });
      resetSession();
      setUser(u);
      return u;
    },
    [resetSession],
  );

  const logout = useCallback(async () => {
    try {
      await post("/auth/logout");
    } finally {
      resetSession();
      setUser(null);
    }
  }, [resetSession]);

  return (
    <AuthContext.Provider value={{ user, loading, login, register, logout }}>
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth(): AuthState {
  const ctx = useContext(AuthContext);
  if (!ctx) throw new Error("useAuth outside AuthProvider");
  return ctx;
}

export function RequireAuth({ children }: { children: ReactNode }) {
  const { user, loading } = useAuth();
  const location = useLocation();
  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center">
        <Spinner />
      </div>
    );
  }
  if (!user) {
    return <Navigate to="/login" state={{ from: location.pathname }} replace />;
  }
  return <>{children}</>;
}
