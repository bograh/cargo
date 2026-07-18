import { useEffect, useRef, useState } from "react";
import { useNavigate, useParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import { post } from "../lib/api";
import type { Org } from "../lib/types";
import { Card, Spinner } from "../components/ui";

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
    <div className="mx-auto max-w-md">
      <Card>
        {error ? (
          <p className="text-sm text-red-400">Could not accept invite: {error}</p>
        ) : (
          <div className="flex items-center gap-3">
            <Spinner />
            <p className="text-sm text-slate-300">Joining organization…</p>
          </div>
        )}
      </Card>
    </div>
  );
}
