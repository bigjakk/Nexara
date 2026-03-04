import { lazy, Suspense } from "react";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { AppShell } from "@/components/layout/AppShell";
import { LoginPage } from "@/features/auth/pages/LoginPage";
import { RegisterPage } from "@/features/auth/pages/RegisterPage";
import { DashboardPage } from "@/features/dashboard/pages/DashboardPage";
import { ClustersListPage } from "@/features/clusters/pages/ClustersListPage";
import { ClusterDetailPage } from "@/features/clusters/pages/ClusterDetailPage";
import { InventoryPage } from "@/features/inventory/pages/InventoryPage";

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
            path: "inventory",
            element: <InventoryPage />,
          },
          {
            path: "console",
            element: (
              <Suspense fallback={<div className="flex h-full items-center justify-center">Loading...</div>}>
                <ConsolePage />
              </Suspense>
            ),
          },
        ],
      },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
