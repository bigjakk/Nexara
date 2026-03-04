import { useEffect } from "react";
import { Outlet } from "react-router-dom";
import { LogOut, LogOutIcon, User } from "lucide-react";

import { Avatar, AvatarFallback } from "@/components/ui/avatar";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Separator } from "@/components/ui/separator";
import { useAuth } from "@/hooks/useAuth";
import { useWebSocketStore } from "@/stores/websocket-store";
import { Sidebar } from "./Sidebar";

function getInitials(name: string): string {
  return name
    .split(" ")
    .map((part) => part[0])
    .filter(Boolean)
    .slice(0, 2)
    .join("")
    .toUpperCase();
}

export function AppShell() {
  const { user, logout, logoutAll } = useAuth();
  const wsConnect = useWebSocketStore((s) => s.connect);
  const wsDisconnect = useWebSocketStore((s) => s.disconnect);

  useEffect(() => {
    wsConnect();
    return () => { wsDisconnect(); };
  }, [wsConnect, wsDisconnect]);

  const handleLogout = () => {
    void logout();
  };

  const handleLogoutAll = () => {
    void logoutAll();
  };

  return (
    <div className="flex h-screen">
      <Sidebar />
      <div className="flex flex-1 flex-col overflow-hidden">
        <header className="flex h-14 items-center justify-end border-b px-4">
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" className="gap-2">
                <Avatar className="h-7 w-7">
                  <AvatarFallback className="text-xs">
                    {user ? getInitials(user.display_name) : "?"}
                  </AvatarFallback>
                </Avatar>
                <span className="text-sm">
                  {user?.display_name ?? "User"}
                </span>
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="w-48">
              <div className="px-2 py-1.5">
                <p className="text-sm font-medium">{user?.display_name}</p>
                <p className="text-xs text-muted-foreground">{user?.email}</p>
              </div>
              <DropdownMenuSeparator />
              <DropdownMenuItem>
                <User className="mr-2 h-4 w-4" />
                Profile
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem onClick={handleLogout}>
                <LogOut className="mr-2 h-4 w-4" />
                Sign Out
              </DropdownMenuItem>
              <DropdownMenuItem onClick={handleLogoutAll}>
                <LogOutIcon className="mr-2 h-4 w-4" />
                Sign Out All Devices
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </header>
        <Separator />
        <main className="flex-1 overflow-auto p-6">
          <Outlet />
        </main>
      </div>
    </div>
  );
}
