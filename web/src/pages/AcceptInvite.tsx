import { useEffect, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post } from "../lib/api";
import type { Org } from "../lib/types";
import { AuthShell } from "../components/AuthShell";
import { Spinner } from "../components/ui";

export default function AcceptInvite() {
  const { token } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const [error, setError] = useState("");
  const attempted = useRef(false);

  useEffect(() => {
    if (attempted.current || !token) return;
    attempted.current = true;
    post<Org>("/invites/accept", { token })
      .then((org) => {
        void qc.invalidateQueries({ queryKey: ["orgs"] });
        navigate(`/orgs/${org.id}`, { replace: true });
      })
      .catch((err) => setError(err instanceof Error ? err.message : "invite invalid"));
  }, [token, navigate, qc]);

  return (
    <AuthShell title="Join organization">
      {error ? (
        <div className="space-y-3">
          <p className="text-sm text-danger">Could not accept invite: {error}</p>
          <Link to="/" className="text-sm text-amber hover:underline">
            Back to dashboard
          </Link>
        </div>
      ) : (
        <div className="flex items-center gap-3">
          <Spinner />
          <p className="text-sm text-muted">Joining organization…</p>
        </div>
      )}
    </AuthShell>
  );
}
