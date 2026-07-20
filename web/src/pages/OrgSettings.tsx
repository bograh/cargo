import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useNavigate, useParams } from "react-router-dom";
import { api, del, patch, post } from "../lib/api";
import type { Invite, Member } from "../lib/types";
import { useOrg } from "../lib/hooks";
import { useAuth } from "../auth";
import { Badge, Button, Card, PageTitle, Select, Spinner } from "../components/ui";

const ROLES = ["owner", "admin", "member", "viewer"];

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
      <h2 className="mb-2 font-semibold">GitHub</h2>
      {!data.configured ? (
        <p className="text-sm text-slate-500">
          The instance admin has not configured a GitHub App yet. Private-repo deploys and push-to-deploy
          are unavailable until then.
        </p>
      ) : data.connected ? (
        <p className="text-sm text-slate-300">
          Connected to <Badge color="green">{data.account_login || "GitHub"}</Badge> — private repos and
          push-to-deploy are active.
        </p>
      ) : isAdmin && data.install_url ? (
        <div className="flex items-center justify-between">
          <p className="text-sm text-slate-400">Install the GitHub App to deploy private repos.</p>
          <a href={data.install_url}>
            <Button>Install GitHub App</Button>
          </a>
        </div>
      ) : (
        <p className="text-sm text-slate-500">Not connected. Ask an org admin to install the GitHub App.</p>
      )}
    </Card>
  );
}

export default function OrgSettings() {
  const { orgId } = useParams();
  const { user } = useAuth();
  const navigate = useNavigate();
  const qc = useQueryClient();
  const { data: orgData } = useOrg(orgId);
  const role = orgData?.role ?? "viewer";
  const isAdmin = role === "owner" || role === "admin";

  const { data: members, isLoading } = useQuery({
    queryKey: ["members", orgId],
    queryFn: () => api<Member[]>(`/orgs/${orgId}/members`),
    enabled: !!orgId,
  });
  const { data: invites } = useQuery({
    queryKey: ["invites", orgId],
    queryFn: () => api<Invite[]>(`/orgs/${orgId}/invites`),
    enabled: !!orgId && isAdmin,
  });

  const [inviteRole, setInviteRole] = useState("member");
  const [inviteLink, setInviteLink] = useState("");
  const [error, setError] = useState("");

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["members", orgId] });
    void qc.invalidateQueries({ queryKey: ["invites", orgId] });
  };
  const onError = (err: unknown) => setError(err instanceof Error ? err.message : "operation failed");

  const createInvite = useMutation({
    mutationFn: () => post<{ token: string }>(`/orgs/${orgId}/invites`, { role: inviteRole }),
    onSuccess: (data) => {
      setInviteLink(`${window.location.origin}/invite/${data.token}`);
      invalidate();
    },
    onError,
  });
  const revokeInvite = useMutation({
    mutationFn: (id: string) => del(`/orgs/${orgId}/invites/${id}`),
    onSuccess: invalidate,
    onError,
  });
  const changeRole = useMutation({
    mutationFn: (v: { userId: string; role: string }) =>
      patch(`/orgs/${orgId}/members/${v.userId}`, { role: v.role }),
    onSuccess: invalidate,
    onError,
  });
  const removeMember = useMutation({
    mutationFn: (userId: string) => del(`/orgs/${orgId}/members/${userId}`),
    onSuccess: invalidate,
    onError,
  });
  const deleteOrg = useMutation({
    mutationFn: () => del(`/orgs/${orgId}`),
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ["orgs"] });
      navigate("/");
    },
    onError,
  });

  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <PageTitle>{orgData?.organization.name} — Settings</PageTitle>
      {error && <p className="text-sm text-red-400">{error}</p>}

      <Card>
        <h2 className="mb-4 font-semibold">Members</h2>
        <table className="w-full text-sm">
          <thead>
            <tr className="text-left text-xs text-slate-500">
              <th className="pb-2">Email</th>
              <th className="pb-2">Role</th>
              <th className="pb-2" />
            </tr>
          </thead>
          <tbody>
            {members?.map((m) => (
              <tr key={m.UserID} className="border-t border-slate-800">
                <td className="py-2">{m.Email}</td>
                <td className="py-2">
                  {isAdmin && m.UserID !== user?.id ? (
                    <Select
                      aria-label={`role for ${m.Email}`}
                      value={m.Role}
                      onChange={(e) => changeRole.mutate({ userId: m.UserID, role: e.target.value })}
                    >
                      {ROLES.map((r) => (
                        <option key={r} value={r}>
                          {r}
                        </option>
                      ))}
                    </Select>
                  ) : (
                    <Badge color="gray">{m.Role}</Badge>
                  )}
                </td>
                <td className="py-2 text-right">
                  {isAdmin && m.UserID !== user?.id && (
                    <Button variant="danger" onClick={() => removeMember.mutate(m.UserID)}>
                      Remove
                    </Button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </Card>

      {isAdmin && (
        <Card>
          <h2 className="mb-4 font-semibold">Invites</h2>
          <div className="flex items-end gap-2">
            <div>
              <label className="block text-xs text-slate-500 mb-1" htmlFor="invite-role">
                Role
              </label>
              <Select id="invite-role" value={inviteRole} onChange={(e) => setInviteRole(e.target.value)}>
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </div>
            <Button onClick={() => createInvite.mutate()} disabled={createInvite.isPending}>
              Create invite link
            </Button>
          </div>
          {inviteLink && (
            <div className="mt-3 flex items-center gap-2 rounded-md bg-slate-950 p-2 text-xs">
              <code className="flex-1 truncate text-slate-300">{inviteLink}</code>
              <Button variant="secondary" onClick={() => navigator.clipboard.writeText(inviteLink)}>
                Copy
              </Button>
            </div>
          )}
          {inviteLink && (
            <p className="mt-1 text-xs text-amber-400">
              This link is shown once — copy it now and share it with your teammate.
            </p>
          )}
          {invites && invites.length > 0 && (
            <ul className="mt-4 space-y-2">
              {invites.map((inv) => (
                <li key={inv.ID} className="flex items-center justify-between text-sm">
                  <span className="text-slate-400">
                    <Badge color="indigo">{inv.Role}</Badge>
                  </span>
                  <Button variant="secondary" onClick={() => revokeInvite.mutate(inv.ID)}>
                    Revoke
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      <GithubCard orgId={orgId!} isAdmin={isAdmin} />

      {role === "owner" && (
        <Card className="border-red-900">
          <h2 className="mb-2 font-semibold text-red-400">Danger zone</h2>
          <p className="mb-3 text-sm text-slate-400">
            Deleting the organization removes all its apps and memberships.
          </p>
          <Button
            variant="danger"
            onClick={() => {
              if (window.confirm("Delete this organization? This cannot be undone.")) {
                deleteOrg.mutate();
              }
            }}
          >
            Delete organization
          </Button>
        </Card>
      )}
    </div>
  );
}
