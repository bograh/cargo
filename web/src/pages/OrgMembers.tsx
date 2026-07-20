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
  Label,
  PageHeader,
  Select,
  Skeleton,
  Textarea,
  useToast,
} from "../components/ui";

const ROLES = ["owner", "admin", "member", "viewer"];

interface InviteResult {
  email?: string;
  sent: boolean;
  link?: string;
  error?: string;
}

function inviteEmail(inv: Invite): string {
  const e = inv.Email;
  if (!e) return "";
  return typeof e === "string" ? e : e.Valid ? e.String : "";
}

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
  const [emails, setEmails] = useState("");
  const [results, setResults] = useState<InviteResult[]>([]);
  const [removing, setRemoving] = useState<Member | null>(null);
  const [revoking, setRevoking] = useState<Invite | null>(null);

  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["members", orgId] });
    void qc.invalidateQueries({ queryKey: ["invites", orgId] });
  };
  const onError = (err: unknown) => toast(err instanceof Error ? err.message : "operation failed", "error");

  const parsedEmails = emails.split(/[\s,]+/).map((e) => e.trim()).filter(Boolean);

  const sendInvites = useMutation({
    mutationFn: () => post<{ results: InviteResult[] }>(`/orgs/${orgId}/invites`, { role: inviteRole, emails: parsedEmails }),
    onSuccess: (data) => {
      setResults(data.results ?? []);
      setEmails("");
      const sent = (data.results ?? []).filter((r) => r.sent).length;
      if (sent > 0) toast(`Sent ${sent} invite${sent === 1 ? "" : "s"}`);
      invalidate();
    },
    onError,
  });
  const createLink = useMutation({
    mutationFn: () => post<{ token: string }>(`/orgs/${orgId}/invites`, { role: inviteRole }),
    onSuccess: (data) => {
      setResults([{ sent: false, link: `${window.location.origin}/invite/${data.token}` }]);
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
          <h2 className="font-display font-semibold">Invite people</h2>
          <p className="mt-1 text-sm text-muted">
            Enter one or more emails to send an invitation, or create a shareable link.
          </p>
          <div className="mt-4 grid gap-3 sm:grid-cols-[1fr_auto]">
            <div>
              <Label htmlFor="invite-emails">Emails</Label>
              <Textarea
                id="invite-emails"
                rows={2}
                placeholder="alice@example.com, bob@example.com"
                value={emails}
                onChange={(e) => setEmails(e.target.value)}
              />
            </div>
            <div>
              <Label htmlFor="invite-role">Role</Label>
              <Select
                id="invite-role"
                className="w-full sm:w-36"
                value={inviteRole}
                onChange={(e) => setInviteRole(e.target.value)}
              >
                {ROLES.map((r) => (
                  <option key={r} value={r}>
                    {r}
                  </option>
                ))}
              </Select>
            </div>
          </div>
          <div className="mt-3 flex flex-wrap items-center gap-2">
            <Button
              onClick={() => sendInvites.mutate()}
              disabled={sendInvites.isPending || parsedEmails.length === 0}
            >
              <Icon name="users" size={14} /> Send invite{parsedEmails.length > 1 ? "s" : ""}
            </Button>
            <Button variant="secondary" onClick={() => createLink.mutate()} disabled={createLink.isPending}>
              <Icon name="link" size={14} /> Create link
            </Button>
          </div>

          {results.length > 0 && (
            <ul className="mt-4 space-y-2">
              {results.map((res, i) => (
                <li key={i} className="rounded-lg border border-border bg-raised px-3 py-2 text-sm">
                  <div className="flex items-center justify-between gap-2">
                    <span className="truncate">{res.email ?? "Shareable link"}</span>
                    {res.sent ? (
                      <Badge tone="live">sent</Badge>
                    ) : (
                      <Badge tone="amber">link</Badge>
                    )}
                  </div>
                  {res.link && (
                    <div className="mt-2 flex items-center gap-2">
                      <code className="flex-1 truncate rounded-md bg-terminal px-2 py-1 font-mono text-xs text-text">
                        {res.link}
                      </code>
                      <Button
                        variant="ghost"
                        aria-label="copy invite link"
                        onClick={() => {
                          void navigator.clipboard?.writeText(res.link!);
                          toast("Invite link copied");
                        }}
                      >
                        <Icon name="copy" size={14} />
                      </Button>
                    </div>
                  )}
                  {res.error && <p className="mt-1 text-xs text-muted">{res.error}</p>}
                </li>
              ))}
            </ul>
          )}
        </Card>
      )}

      {isAdmin && (
        <Card>
          <h2 className="mb-3 font-display font-semibold">Pending invites</h2>
          {invites && invites.length > 0 ? (
            <ul className="divide-y divide-border">
              {invites.map((inv) => (
                <li key={inv.ID} className="flex items-center justify-between gap-2 py-2.5 text-sm first:pt-0 last:pb-0">
                  <span className="flex items-center gap-2 truncate">
                    <Icon name={inviteEmail(inv) ? "users" : "link"} size={14} className="shrink-0 text-muted" />
                    <span className="truncate">{inviteEmail(inv) || "Shareable link"}</span>
                    <Badge tone="amber">{inv.Role}</Badge>
                  </span>
                  <Button variant="secondary" onClick={() => setRevoking(inv)}>
                    Revoke
                  </Button>
                </li>
              ))}
            </ul>
          ) : (
            <p className="text-sm text-muted">No pending invites.</p>
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
