import { useParams } from "react-router-dom";
import { PageHeader } from "../components/ui";
import { DatabasesTab } from "../components/DatabasesTab";

export default function OrgDatabases() {
  const { orgId } = useParams();
  if (!orgId) return null;
  return (
    <div>
      <PageHeader eyebrow="org" title="Databases" />
      <DatabasesTab orgId={orgId} />
    </div>
  );
}
