import { Route, Routes } from "react-router-dom";
import { RequireAuth } from "./auth";
import { Shell } from "./components/layout/Shell";
import Login from "./pages/Login";
import Register from "./pages/Register";
import Home from "./pages/Home";
import NewOrg from "./pages/NewOrg";
import OrgApps from "./pages/OrgApps";
import OrgDatabases from "./pages/OrgDatabases";
import OrgMembers from "./pages/OrgMembers";
import OrgSettings from "./pages/OrgSettings";
import NewApp from "./pages/NewApp";
import AppOverview from "./pages/AppOverview";
import AppDeployments from "./pages/AppDeployments";
import AppLogs from "./pages/AppLogs";
import AppEnv from "./pages/AppEnv";
import AppDomains from "./pages/AppDomains";
import AppSettings from "./pages/AppSettings";
import DeploymentLogs from "./pages/DeploymentLogs";
import AcceptInvite from "./pages/AcceptInvite";
import Admin from "./pages/Admin";
import NotFound from "./pages/NotFound";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route path="/invite/:token" element={<AcceptInvite />} />
      <Route element={<RequireAuth><Shell /></RequireAuth>}>
        <Route path="/" element={<Home />} />
        <Route path="/orgs/new" element={<NewOrg />} />
        <Route path="/orgs/:orgId" element={<OrgApps />} />
        <Route path="/orgs/:orgId/databases" element={<OrgDatabases />} />
        <Route path="/orgs/:orgId/members" element={<OrgMembers />} />
        <Route path="/orgs/:orgId/settings" element={<OrgSettings />} />
        <Route path="/orgs/:orgId/apps/new" element={<NewApp />} />
        <Route path="/apps/:appId" element={<AppOverview />} />
        <Route path="/apps/:appId/deployments" element={<AppDeployments />} />
        <Route path="/apps/:appId/logs" element={<AppLogs />} />
        <Route path="/apps/:appId/env" element={<AppEnv />} />
        <Route path="/apps/:appId/domains" element={<AppDomains />} />
        <Route path="/apps/:appId/settings" element={<AppSettings />} />
        <Route path="/deployments/:deploymentId" element={<DeploymentLogs />} />
        <Route path="/admin" element={<Admin />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
