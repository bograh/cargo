import { useQuery } from "@tanstack/react-query";
import { Navigate } from "react-router-dom";
import { api } from "../lib/api";
import { useAuth } from "../auth";
import type { User } from "../lib/types";
import { Badge, Card, PageTitle, Spinner } from "../components/ui";

interface AdminOrg {
  id: string;
  name: string;
  slug: string;
}

export default function Admin() {
  const { user } = useAuth();
  const isAdmin = !!user?.is_instance_admin;
  const { data: users, isLoading } = useQuery({
    queryKey: ["admin", "users"],
    queryFn: () => api<User[]>("/admin/users"),
    enabled: isAdmin,
  });
  const { data: orgs } = useQuery({
    queryKey: ["admin", "orgs"],
    queryFn: () => api<AdminOrg[]>("/admin/orgs"),
    enabled: isAdmin,
  });

  if (!isAdmin) {
    return <Navigate to="/" replace />;
  }
  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      <PageTitle>Instance administration</PageTitle>
      <Card>
        <h2 className="mb-3 font-semibold">Users</h2>
        <ul className="space-y-2 text-sm">
          {users?.map((u) => (
            <li key={u.id} className="flex items-center justify-between border-t border-slate-800 pt-2 first:border-0 first:pt-0">
              <span>{u.email}</span>
              {u.is_instance_admin && <Badge color="indigo">instance admin</Badge>}
            </li>
          ))}
        </ul>
      </Card>
      <Card>
        <h2 className="mb-3 font-semibold">Organizations</h2>
        <ul className="space-y-2 text-sm">
          {orgs?.map((o) => (
            <li key={o.id} className="flex items-center justify-between border-t border-slate-800 pt-2 first:border-0 first:pt-0">
              <span>{o.name}</span>
              <span className="text-slate-500">{o.slug}</span>
            </li>
          ))}
        </ul>
      </Card>
    </div>
  );
}
