import { Route, Routes } from "react-router-dom";
import { RequireAuth } from "./auth";
import { Shell } from "./components/layout/Shell";
import Login from "./pages/Login";
import Register from "./pages/Register";
import Home from "./pages/Home";
import NewOrg from "./pages/NewOrg";
import OrgApps from "./pages/OrgApps";
import OrgSettings from "./pages/OrgSettings";
import NewApp from "./pages/NewApp";
import AppDetail from "./pages/AppDetail";
import DeploymentLogs from "./pages/DeploymentLogs";
import AcceptInvite from "./pages/AcceptInvite";
import Admin from "./pages/Admin";
import NotFound from "./pages/NotFound";

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route element={<RequireAuth><Shell /></RequireAuth>}>
        <Route path="/" element={<Home />} />
        <Route path="/orgs/new" element={<NewOrg />} />
        <Route path="/orgs/:orgId" element={<OrgApps />} />
        <Route path="/orgs/:orgId/settings" element={<OrgSettings />} />
        <Route path="/orgs/:orgId/apps/new" element={<NewApp />} />
        <Route path="/apps/:appId" element={<AppDetail />} />
        <Route path="/deployments/:deploymentId" element={<DeploymentLogs />} />
        <Route path="/invite/:token" element={<AcceptInvite />} />
        <Route path="/admin" element={<Admin />} />
        <Route path="*" element={<NotFound />} />
      </Route>
    </Routes>
  );
}
