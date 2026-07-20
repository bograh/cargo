import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api, del } from "../lib/api";
import { useOrg } from "../lib/hooks";
import { Badge, Button, Card, ConfirmModal, Icon, PageHeader, useToast } from "../components/ui";

interface GithubStatus {
  configured: boolean;
  connected: boolean;
  account_login: string;
  install_url?: string;
}

function GithubCard({ orgId, isAdmin }: { orgId: string; isAdmin: boolean }) {
  const { data } = useQuery({
    queryKey: ["github", orgId],
    queryFn: () => api<GithubStatus>(`/orgs/${orgId}/github`),
  });
  if (!data) return null;
  return (
    <Card>
      <h2 className="mb-2 font-display font-semibold">GitHub</h2>
      {!data.configured ? (
        <p className="text-sm text-muted">
          The instance admin has not configured a GitHub App yet. Private-repo deploys and push-to-deploy
          are unavailable until then.
        </p>
      ) : data.connected ? (
        <p className="flex items-center gap-1.5 text-sm text-text">
          Connected to <Badge tone="live">{data.account_login || "GitHub"}</Badge> — private repos and
          push-to-deploy are active.
        </p>
      ) : isAdmin && data.install_url ? (
        <div className="flex items-center justify-between gap-4">
          <p className="text-sm text-muted">Install the GitHub App to deploy private repos.</p>
          <a href={data.install_url}>
            <Button>Install GitHub App</Button>
          </a>
        </div>
      ) : (
        <p className="text-sm text-muted">Not connected. Ask an org admin to install the GitHub App.</p>
      )}
    </Card>
  );
}

export default function OrgSettings() {
  const { orgId } = useParams();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const toast = useToast();
  const { data: orgData } = useOrg(orgId);
  const role = orgData?.role ?? "viewer";
  const isAdmin = role === "owner" || role === "admin";
  const orgName = orgData?.organization.name ?? "";

  const [confirming, setConfirming] = useState(false);

  const deleteOrg = useMutation({
    mutationFn: () => del(`/orgs/${orgId}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["orgs"] });
      toast("Organization deleted");
      navigate("/");
    },
    onError: (err) => toast(err instanceof Error ? err.message : "delete failed", "error"),
  });

  return (
    <div className="space-y-6">
      <PageHeader eyebrow="org" title="Org Settings" />

      <GithubCard orgId={orgId!} isAdmin={isAdmin} />

      {role === "owner" && (
        <Card className="border-danger/40">
          <h2 className="font-display text-lg font-semibold text-danger">Danger zone</h2>
          <p className="mt-1 text-sm text-muted">
            Deleting an organization removes all its apps, databases, and deployments.
          </p>
          <Button variant="danger" className="mt-4" onClick={() => setConfirming(true)}>
            <Icon name="trash" size={14} /> Delete organization
          </Button>
        </Card>
      )}

      {confirming && (
        <ConfirmModal
          title={`Delete ${orgName}?`}
          body="All apps, databases, and deployments in this organization will be destroyed."
          requireText={orgName}
          busy={deleteOrg.isPending}
          onConfirm={() => deleteOrg.mutate()}
          onClose={() => setConfirming(false)}
        />
      )}
    </div>
  );
}
