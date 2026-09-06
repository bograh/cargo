import { useCallback, useEffect, useRef, useState, type FormEvent } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { api, post } from "../lib/api";
import type { Org } from "../lib/types";
import { useAuth } from "../auth";
import { AuthShell } from "../components/AuthShell";
import { Badge, Button, FieldError, Input, Label, Spinner } from "../components/ui";

interface Preview {
  org_name: string;
  role: string;
  email: string;
}

export default function AcceptInvite() {
  const { token } = useParams();
  const { user, loading, login, register } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();

  const [preview, setPreview] = useState<Preview | null>(null);
  const [previewError, setPreviewError] = useState("");
  const [mode, setMode] = useState<"register" | "login">("register");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const accepting = useRef(false);

  useEffect(() => {
    if (!token) return;
    api<Preview>(`/invites/${token}`)
      .then((p) => {
        setPreview(p);
        if (p.email) setEmail(p.email);
      })
      .catch((e) => setPreviewError(e instanceof Error ? e.message : "invite is invalid or expired"));
  }, [token]);

  const accept = useCallback(async () => {
    if (accepting.current) return;
    accepting.current = true;
    try {
      const org = await post<Org>("/invites/accept", { token });
      await qc.invalidateQueries({ queryKey: ["orgs"] });
      navigate(`/orgs/${org.id}`, { replace: true });
    } catch (e) {
      accepting.current = false;
      setError(e instanceof Error ? e.message : "could not accept the invite");
    }
  }, [token, qc, navigate]);

  // Already signed in → accept immediately once the preview validates.
  useEffect(() => {
    if (loading || !user || !preview || previewError) return;
    void accept();
  }, [loading, user, preview, previewError, accept]);

  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setError("");
    try {
      if (mode === "register") await register(email, password, token);
      else await login(email, password);
      await accept();
    } catch (err) {
      setError(err instanceof Error ? err.message : "authentication failed");
      setBusy(false);
    }
  }

  if (previewError) {
    return (
      <AuthShell title="Invitation">
        <p className="text-sm text-danger">{previewError}</p>
        <Link to="/" className="mt-3 inline-block text-sm text-amber hover:underline">
          Go to dashboard
        </Link>
      </AuthShell>
    );
  }

  if (!preview || (user && !error)) {
    return (
      <AuthShell title="Invitation">
        <div className="flex items-center gap-3">
          <Spinner />
          <p className="text-sm text-muted">
            {preview ? `Joining ${preview.org_name}…` : "Loading invitation…"}
          </p>
        </div>
        {error && <p className="mt-3 text-sm text-danger">{error}</p>}
      </AuthShell>
    );
  }

  // Unauthenticated: show what they're joining, then register or log in.
  return (
    <AuthShell title={mode === "register" ? "Create your account" : "Log in to accept"}>
      <div className="mb-4 rounded-lg border border-border bg-raised px-3 py-2.5 text-sm">
        You've been invited to join{" "}
        <span className="font-display font-semibold text-text">{preview.org_name}</span> as{" "}
        <Badge tone="amber">{preview.role}</Badge>
      </div>
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="email">Email</Label>
          <Input
            id="email"
            type="email"
            value={email}
            onChange={(e) => setEmail(e.target.value)}
            readOnly={!!preview.email}
            required
          />
        </div>
        <div>
          <Label htmlFor="password">Password</Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            minLength={mode === "register" ? 10 : undefined}
            required
          />
          {mode === "register" && <p className="mt-1 text-xs text-muted">At least 10 characters.</p>}
          <FieldError message={error} />
        </div>
        <Button type="submit" className="w-full" disabled={busy}>
          {mode === "register" ? "Create account & join" : "Log in & join"}
        </Button>
      </form>
      <p className="mt-4 text-center text-sm text-muted">
        {mode === "register" ? "Already have an account? " : "Need an account? "}
        <button
          type="button"
          onClick={() => {
            setMode(mode === "register" ? "login" : "register");
            setError("");
          }}
          className="text-amber hover:underline"
        >
          {mode === "register" ? "Log in" : "Register"}
        </button>
      </p>
    </AuthShell>
  );
}
