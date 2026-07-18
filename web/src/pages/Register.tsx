import { useState, type FormEvent } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useAuth } from "../auth";
import { Button, FieldError, Input, Label } from "../components/ui";
import { AuthShell } from "./Login";

export default function Register() {
  const { register } = useAuth();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: FormEvent) {
    e.preventDefault();
    if (password.length < 10) {
      setError("password must be at least 10 characters");
      return;
    }
    setBusy(true);
    setError("");
    try {
      await register(email, password);
      navigate("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "registration failed");
    } finally {
      setBusy(false);
    }
  }

  return (
    <AuthShell title="Create your account">
      <form onSubmit={submit} className="space-y-4">
        <div>
          <Label htmlFor="email">Email</Label>
          <Input id="email" type="email" value={email} onChange={(e) => setEmail(e.target.value)} required />
        </div>
        <div>
          <Label htmlFor="password">Password (min 10 characters)</Label>
          <Input
            id="password"
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            required
          />
          <FieldError message={error} />
        </div>
        <Button type="submit" className="w-full" disabled={busy}>
          Register
        </Button>
        <p className="text-center text-sm text-slate-400">
          Already have an account?{" "}
          <Link to="/login" className="text-indigo-400 hover:underline">
            Log in
          </Link>
        </p>
      </form>
    </AuthShell>
  );
}
