import { Navigate } from "react-router-dom";
import { useOrgs } from "../components/OrgSwitcher";
import { Spinner } from "../components/ui";

export default function Home() {
  const { data: orgs, isLoading } = useOrgs();
  if (isLoading) {
    return (
      <div className="flex justify-center py-20">
        <Spinner />
      </div>
    );
  }
  if (!orgs || orgs.length === 0) {
    return <Navigate to="/orgs/new" replace />;
  }
  return <Navigate to={`/orgs/${orgs[0].id}`} replace />;
}
