import { lazy, Suspense } from "react";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { AppShell } from "@/components/layout/AppShell";
import { LoginPage } from "@/features/auth/pages/LoginPage";
import { RegisterPage } from "@/features/auth/pages/RegisterPage";
import { DashboardPage } from "@/features/dashboard/pages/DashboardPage";
import { ClustersListPage } from "@/features/clusters/pages/ClustersListPage";
import { ClusterDetailPage } from "@/features/clusters/pages/ClusterDetailPage";
import { NodeDetailPage } from "@/features/clusters/pages/NodeDetailPage";
import { InventoryPage } from "@/features/inventory/pages/InventoryPage";
import { VMDetailPage } from "@/features/vms/pages/VMDetailPage";
import { StoragePage } from "@/features/storage/pages/StoragePage";
import { BackupDashboardPage } from "@/features/backup/pages/BackupDashboardPage";
import { AuditLogPage } from "@/features/audit/pages/AuditLogPage";
import { UsersPage } from "@/features/admin/pages/UsersPage";
import { RolesPage } from "@/features/admin/pages/RolesPage";
import { LDAPPage } from "@/features/admin/pages/LDAPPage";
import { OIDCPage } from "@/features/admin/pages/OIDCPage";
import { BrandingPage } from "@/features/admin/pages/BrandingPage";
import { OIDCCallbackPage } from "@/features/auth/pages/OIDCCallbackPage";
import { SecurityPage } from "@/features/settings/pages/SecurityPage";
import { AppearancePage } from "@/features/settings/pages/AppearancePage";
import { SecurityDashboardPage } from "@/features/security/pages/SecurityDashboardPage";
import { AlertsPage } from "@/features/alerts/pages/AlertsPage";
import { ReportsPage } from "@/features/reports/pages/ReportsPage";
import { TopologyPage } from "@/features/topology/pages/TopologyPage";

const ConsolePage = lazy(() =>
  import("@/features/console/pages/ConsolePage").then((m) => ({
    default: m.ConsolePage,
  })),
);

const router = createBrowserRouter([
  {
    path: "/login",
    element: <LoginPage />,
  },
  {
    path: "/register",
    element: <RegisterPage />,
  },
  {
    path: "/oidc-callback",
    element: <OIDCCallbackPage />,
  },
  {
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppShell />,
        children: [
          {
            index: true,
            element: <DashboardPage />,
          },
          {
            path: "clusters",
            element: <ClustersListPage />,
          },
          {
            path: "clusters/:clusterId",
            element: <ClusterDetailPage />,
          },
          {
            path: "clusters/:clusterId/nodes/:nodeId",
            element: <NodeDetailPage />,
          },
          {
            path: "inventory",
            element: <InventoryPage />,
          },
          {
            path: "inventory/:kind/:clusterId/:vmId",
            element: <VMDetailPage />,
          },
          {
            path: "storage",
            element: <StoragePage />,
          },
          {
            path: "backup",
            element: <BackupDashboardPage />,
          },
          {
            path: "audit-log",
            element: <AuditLogPage />,
          },
          {
            path: "console",
            element: (
              <Suspense fallback={<div className="flex h-full items-center justify-center">Loading...</div>}>
                <ConsolePage />
              </Suspense>
            ),
          },
          {
            path: "admin/users",
            element: <UsersPage />,
          },
          {
            path: "admin/roles",
            element: <RolesPage />,
          },
          {
            path: "admin/ldap",
            element: <LDAPPage />,
          },
          {
            path: "admin/oidc",
            element: <OIDCPage />,
          },
          {
            path: "admin/branding",
            element: <BrandingPage />,
          },
          {
            path: "alerts",
            element: <AlertsPage />,
          },
          {
            path: "reports",
            element: <ReportsPage />,
          },
          {
            path: "topology",
            element: <TopologyPage />,
          },
          {
            path: "security",
            element: <SecurityDashboardPage />,
          },
{
            path: "settings/security",
            element: <SecurityPage />,
          },
          {
            path: "settings/appearance",
            element: <AppearancePage />,
          },
        ],
      },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
