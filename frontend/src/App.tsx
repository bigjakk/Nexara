import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { ProtectedRoute } from "@/components/auth/ProtectedRoute";
import { AppShell } from "@/components/layout/AppShell";
import { LoginPage } from "@/features/auth/pages/LoginPage";
import { RegisterPage } from "@/features/auth/pages/RegisterPage";
import { DashboardPage } from "@/features/dashboard/pages/DashboardPage";
import { ClustersListPage } from "@/features/clusters/pages/ClustersListPage";
import { ClusterDetailPage } from "@/features/clusters/pages/ClusterDetailPage";
import { InventoryPage } from "@/features/inventory/pages/InventoryPage";

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
        ],
      },
    ],
  },
]);

export default function App() {
  return <RouterProvider router={router} />;
}
