import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";

export function Shell() {
  return (
    <div className="min-h-screen bg-bg text-text">
      <Sidebar />
      <div className="pt-14 md:pl-16 md:pt-0 lg:pl-60">
        <main className="mx-auto max-w-6xl px-4 py-6 sm:px-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
