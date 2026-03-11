import { lazy, Suspense } from "react";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { AppShell } from "@/components/layout/AppShell";
import { PageLoader } from "@/components/PageLoader";
import { LoginPage } from "@/features/auth/pages/LoginPage";
import { RegisterPage } from "@/features/auth/pages/RegisterPage";
import { DashboardPage } from "@/features/dashboard/pages/DashboardPage";
import { OIDCCallbackPage } from "@/features/auth/pages/OIDCCallbackPage";

// Lazy-loaded route pages — keeps initial bundle small.
const ClustersListPage = lazy(() =>
  import("@/features/clusters/pages/ClustersListPage").then((m) => ({
    default: m.ClustersListPage,
  })),
);
const ClusterDetailPage = lazy(() =>
  import("@/features/clusters/pages/ClusterDetailPage").then((m) => ({
    default: m.ClusterDetailPage,
  })),
);
const NodeDetailPage = lazy(() =>
  import("@/features/clusters/pages/NodeDetailPage").then((m) => ({
    default: m.NodeDetailPage,
  })),
);
const InventoryPage = lazy(() =>
  import("@/features/inventory/pages/InventoryPage").then((m) => ({
    default: m.InventoryPage,
  })),
);
const VMDetailPage = lazy(() =>
  import("@/features/vms/pages/VMDetailPage").then((m) => ({
    default: m.VMDetailPage,
  })),
);
const StoragePage = lazy(() =>
  import("@/features/storage/pages/StoragePage").then((m) => ({
    default: m.StoragePage,
  })),
);
const BackupDashboardPage = lazy(() =>
  import("@/features/backup/pages/BackupDashboardPage").then((m) => ({
    default: m.BackupDashboardPage,
  })),
);
const EventsPage = lazy(() =>
  import("@/features/events/pages/EventsPage").then((m) => ({
    default: m.EventsPage,
  })),
);
const ConsolePage = lazy(() =>
  import("@/features/console/pages/ConsolePage").then((m) => ({
    default: m.ConsolePage,
  })),
);
const UsersPage = lazy(() =>
  import("@/features/admin/pages/UsersPage").then((m) => ({
    default: m.UsersPage,
  })),
);
const RolesPage = lazy(() =>
  import("@/features/admin/pages/RolesPage").then((m) => ({
    default: m.RolesPage,
  })),
);
const LDAPPage = lazy(() =>
  import("@/features/admin/pages/LDAPPage").then((m) => ({
    default: m.LDAPPage,
  })),
);
const OIDCPage = lazy(() =>
  import("@/features/admin/pages/OIDCPage").then((m) => ({
    default: m.OIDCPage,
  })),
);
const BrandingPage = lazy(() =>
  import("@/features/admin/pages/BrandingPage").then((m) => ({
    default: m.BrandingPage,
  })),
);
const SecurityPage = lazy(() =>
  import("@/features/settings/pages/SecurityPage").then((m) => ({
    default: m.SecurityPage,
  })),
);
const AppearancePage = lazy(() =>
  import("@/features/settings/pages/AppearancePage").then((m) => ({
    default: m.AppearancePage,
  })),
);
const ProfilePage = lazy(() =>
  import("@/features/settings/pages/ProfilePage").then((m) => ({
    default: m.ProfilePage,
  })),
);
const SecurityDashboardPage = lazy(() =>
  import("@/features/security/pages/SecurityDashboardPage").then((m) => ({
    default: m.SecurityDashboardPage,
  })),
);
const AlertsPage = lazy(() =>
  import("@/features/alerts/pages/AlertsPage").then((m) => ({
    default: m.AlertsPage,
  })),
);
const ReportsPage = lazy(() =>
  import("@/features/reports/pages/ReportsPage").then((m) => ({
    default: m.ReportsPage,
  })),
);
const TopologyPage = lazy(() =>
  import("@/features/topology/pages/TopologyPage").then((m) => ({
    default: m.TopologyPage,
  })),
);

function LazyPage({ children }: { children: React.ReactNode }) {
  return <Suspense fallback={<PageLoader />}>{children}</Suspense>;
}

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
            element: <LazyPage><ClustersListPage /></LazyPage>,
          },
          {
            path: "clusters/:clusterId",
            element: <LazyPage><ClusterDetailPage /></LazyPage>,
          },
          {
            path: "clusters/:clusterId/nodes/:nodeId",
            element: <LazyPage><NodeDetailPage /></LazyPage>,
          },
          {
            path: "inventory",
            element: <LazyPage><InventoryPage /></LazyPage>,
          },
          {
            path: "inventory/:kind/:clusterId/:vmId",
            element: <LazyPage><VMDetailPage /></LazyPage>,
          },
          {
            path: "storage",
            element: <LazyPage><StoragePage /></LazyPage>,
          },
          {
            path: "backup",
            element: <LazyPage><BackupDashboardPage /></LazyPage>,
          },
          {
            path: "events",
            element: <LazyPage><EventsPage /></LazyPage>,
          },
          {
            path: "console",
            element: <LazyPage><ConsolePage /></LazyPage>,
          },
          {
            path: "admin/users",
            element: <LazyPage><UsersPage /></LazyPage>,
          },
          {
            path: "admin/roles",
            element: <LazyPage><RolesPage /></LazyPage>,
          },
          {
            path: "admin/ldap",
            element: <LazyPage><LDAPPage /></LazyPage>,
          },
          {
            path: "admin/oidc",
            element: <LazyPage><OIDCPage /></LazyPage>,
          },
          {
            path: "admin/branding",
            element: <LazyPage><BrandingPage /></LazyPage>,
          },
          {
            path: "alerts",
            element: <LazyPage><AlertsPage /></LazyPage>,
          },
          {
            path: "reports",
            element: <LazyPage><ReportsPage /></LazyPage>,
          },
          {
            path: "topology",
            element: <LazyPage><TopologyPage /></LazyPage>,
          },
          {
            path: "security",
            element: <LazyPage><SecurityDashboardPage /></LazyPage>,
          },
          {
            path: "settings/profile",
            element: <LazyPage><ProfilePage /></LazyPage>,
          },
          {
            path: "settings/security",
            element: <LazyPage><SecurityPage /></LazyPage>,
          },
          {
            path: "settings/appearance",
            element: <LazyPage><AppearancePage /></LazyPage>,
          },
        ],
      },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
