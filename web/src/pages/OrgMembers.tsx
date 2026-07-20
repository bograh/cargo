import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useParams } from "react-router-dom";
import { api, del, patch, post } from "../lib/api";
import type { Invite, Member } from "../lib/types";
import { useOrg } from "../lib/hooks";
import { useAuth } from "../auth";
import {
  Badge,
  Button,
  Card,
  ConfirmModal,
  EmptyState,
  Icon,
  PageHeader,
  Select,
  Skeleton,
  useToast,
} from "../components/ui";

const ROLES = ["owner", "admin", "member", "viewer"];

export default function OrgMembers() {
  const { orgId } = useParams();
  const { user } = useAuth();
  const qc = useQueryClient();
  const toast = useToast();
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
  const [removing, setRemoving] = useState<Member | null>(null);
  const [revoking, setRevoking] = useState<Invite | null>(null);

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["members", orgId] });
    void qc.invalidateQueries({ queryKey: ["invites", orgId] });
  };
  const onError = (err: unknown) => toast(err instanceof Error ? err.message : "operation failed", "error");

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
    onSuccess: () => {
      setRevoking(null);
      invalidate();
    },
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
    onSuccess: () => {
      setRemoving(null);
      invalidate();
    },
    onError,
  });

  return (
    <div className="space-y-6">
      <PageHeader eyebrow="org" title="Members" />

      <Card>
        {isLoading ? (
          <div className="space-y-2">
            <Skeleton className="h-8" />
            <Skeleton className="h-8" />
          </div>
        ) : (
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted">
                <th className="pb-2">Email</th>
                <th className="pb-2">Role</th>
                <th className="pb-2" />
              </tr>
            </thead>
            <tbody>
              {members?.map((m) => (
                <tr key={m.UserID} className="border-t border-border">
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
                      <Badge tone="neutral">{m.Role}</Badge>
                    )}
                  </td>
                  <td className="py-2 text-right">
                    {isAdmin && m.UserID !== user?.id && (
                      <Button variant="danger" onClick={() => setRemoving(m)}>
                        Remove
                      </Button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>

      {isAdmin && (
        <Card>
          <h2 className="mb-4 font-display font-semibold">Invites</h2>
          <div className="flex items-end gap-2">
            <div>
              <label className="mb-1 block font-mono text-[0.68rem] uppercase tracking-[0.12em] text-muted" htmlFor="invite-role">
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
            <>
              <div className="mt-3 flex items-center gap-2 rounded-md bg-terminal p-2 text-xs">
                <code className="flex-1 truncate font-mono text-text">{inviteLink}</code>
                <Button
                  variant="secondary"
                  onClick={() => {
                    void navigator.clipboard?.writeText(inviteLink);
                    toast("Invite link copied");
                  }}
                >
                  <Icon name="copy" size={14} /> Copy
                </Button>
              </div>
              <p className="mt-1 text-xs text-amber">
                This link is shown once — copy it now and share it with your teammate.
              </p>
            </>
          )}
          {invites && invites.length > 0 && (
            <ul className="mt-4 space-y-2">
              {invites.map((inv) => (
                <li key={inv.ID} className="flex items-center justify-between text-sm">
                  <Badge tone="amber">{inv.Role}</Badge>
                  <Button variant="secondary" onClick={() => setRevoking(inv)}>
                    Revoke
                  </Button>
                </li>
              ))}
            </ul>
          )}
          {invites && invites.length === 0 && !inviteLink && (
            <p className="mt-4 text-sm text-muted">No pending invites.</p>
          )}
        </Card>
      )}

      {members && members.length === 0 && !isLoading && (
        <EmptyState title="No members" hint="Invite teammates to collaborate on this organization." />
      )}

      {removing && (
        <ConfirmModal
          title={`Remove ${removing.Email}?`}
          body="They will lose access to this organization and all of its apps."
          confirmLabel="Remove"
          busy={removeMember.isPending}
          onConfirm={() => removeMember.mutate(removing.UserID)}
          onClose={() => setRemoving(null)}
        />
      )}
      {revoking && (
        <ConfirmModal
          title="Revoke invite?"
          body="The invite link will stop working immediately."
          confirmLabel="Revoke"
          busy={revokeInvite.isPending}
          onConfirm={() => revokeInvite.mutate(revoking.ID)}
          onClose={() => setRevoking(null)}
        />
      )}
    </div>
  );
}
