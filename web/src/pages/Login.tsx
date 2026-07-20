import { useState, type FormEvent } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useAuth } from "../auth";
import { AuthShell } from "../components/AuthShell";
import { Button, FieldError, Input, Label } from "../components/ui";

export { AuthShell } from "../components/AuthShell";

export default function Login() {
  const { login } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [searchParams] = useSearchParams();
  const ssoError = searchParams.get("error") === "oidc";
  const { data: providers } = useQuery({
    queryKey: ["auth", "providers"],
    queryFn: () => api<{ password: boolean; oidc: boolean }>("/auth/providers"),
  });

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      await login(email, password);
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "login failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Log in">
      {ssoError && (
        <p className="mb-4 rounded-lg border border-danger/40 bg-danger-tint px-3 py-2 text-sm text-danger">
          SSO sign-in failed. Try again or use your password.
        </p>
      )}
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
        <div>
          <Label htmlFor="password">Password</Label>
          <Input id="password" type="password" value={password} onChange={(e) => setPassword(e.target.value)} required />
          <FieldError message={error} />
        </div>
        <Button type="submit" className="w-full" disabled={busy}>
          Log in
        </Button>
        {providers?.oidc && (
          <a
            href="/api/v1/auth/oidc/start"
            className="block w-full rounded-lg border border-border bg-raised px-3 py-1.5 text-center text-sm font-medium text-text transition-colors duration-150 hover:border-amber-dim"
          >
            Sign in with SSO
          </a>
        )}
        <p className="text-center text-sm text-muted">
          No account?{" "}
          <Link to="/register" className="text-amber hover:underline">
            Register
          </Link>
        </p>
      </form>
    </AuthShell>
  );
}
