import { Link, Outlet, Route, Routes, useNavigate } from "react-router-dom";
import { RequireAuth, useAuth } from "./auth";
import { OrgSwitcher } from "./components/OrgSwitcher";
import Login from "./pages/Login";
import Register from "./pages/Register";
import Home from "./pages/Home";
import NewOrg from "./pages/NewOrg";
import OrgDashboard from "./pages/OrgDashboard";
import OrgSettings from "./pages/OrgSettings";
import NewApp from "./pages/NewApp";
import AppDetail from "./pages/AppDetail";
import DeploymentLogs from "./pages/DeploymentLogs";
import AcceptInvite from "./pages/AcceptInvite";
import Admin from "./pages/Admin";

function Shell() {
  const { user, logout } = useAuth();
  const navigate = useNavigate();
  return (
    <div className="min-h-screen bg-slate-950 text-slate-100">
      <header className="border-b border-slate-800 bg-slate-900/70">
        <div className="mx-auto max-w-5xl px-4 py-3 flex items-center gap-4">
          <Link to="/" className="text-lg font-bold tracking-tight text-indigo-400">
            Cargo
          </Link>
          <OrgSwitcher />
          <div className="ml-auto flex items-center gap-3 text-sm text-slate-400">
            {user?.is_instance_admin && (
              <Link to="/admin" className="hover:text-slate-200">
                Admin
              </Link>
            )}
            <span>{user?.email}</span>
            <button
              className="hover:text-slate-200"
              onClick={() => logout().then(() => navigate("/login"))}
            >
              Log out
            </button>
          </div>
        </div>
      </header>
      <main className="mx-auto max-w-5xl px-4 py-8">
        <Outlet />
      </main>
    </div>
  );
}

export default function App() {
  return (
    <Routes>
      <Route path="/login" element={<Login />} />
      <Route path="/register" element={<Register />} />
      <Route
        element={
          <RequireAuth>
            <Shell />
          </RequireAuth>
        }
      >
        <Route path="/" element={<Home />} />
        <Route path="/orgs/new" element={<NewOrg />} />
        <Route path="/orgs/:orgId" element={<OrgDashboard />} />
        <Route path="/orgs/:orgId/settings" element={<OrgSettings />} />
        <Route path="/orgs/:orgId/apps/new" element={<NewApp />} />
        <Route path="/apps/:appId" element={<AppDetail />} />
        <Route path="/deployments/:deploymentId" element={<DeploymentLogs />} />
        <Route path="/invite/:token" element={<AcceptInvite />} />
        <Route path="/admin" element={<Admin />} />
      </Route>
    </Routes>
  );
}
