import { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Trash2 } from "lucide-react";
import {
  useUserRoles,
  useRoles,
  useAssignRole,
  useRevokeRole,
} from "../api/rbac-queries";

interface RoleAssignDialogProps {
  userId: string;
  open: boolean;
  onOpenChange: (open: boolean) => void;
}

export function RoleAssignDialog({
  userId,
  open,
  onOpenChange,
}: RoleAssignDialogProps) {
  const { data: userRoles, isLoading: rolesLoading } = useUserRoles(userId);
  const { data: allRoles } = useRoles();
  const assignRole = useAssignRole();
  const revokeRole = useRevokeRole();

  const [selectedRoleId, setSelectedRoleId] = useState("");

  const handleAssign = () => {
    if (!selectedRoleId) return;
    assignRole.mutate(
      {
        userId,
        role_id: selectedRoleId,
        scope_type: "global",
      },
      {
        onSuccess: () => { setSelectedRoleId(""); },
      },
    );
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg">
        <DialogHeader>
          <DialogTitle>Manage Role Assignments</DialogTitle>
        </DialogHeader>

        <div className="space-y-4">
          {/* Current roles */}
          <div className="space-y-2">
            <h3 className="text-sm font-medium">Assigned Roles</h3>
            {rolesLoading ? (
              <p className="text-sm text-muted-foreground">Loading...</p>
            ) : userRoles?.length === 0 ? (
              <p className="text-sm text-muted-foreground">No roles assigned</p>
            ) : (
              <div className="space-y-2">
                {userRoles?.map((ur) => (
                  <div
                    key={ur.id}
                    className="flex items-center justify-between rounded-md border px-3 py-2"
                  >
                    <div className="flex items-center gap-2">
                      <span className="font-medium">{ur.role_name}</span>
                      <Badge variant="outline" className="text-xs">
                        {ur.scope_type}
                      </Badge>
                      {ur.is_builtin && (
                        <Badge variant="secondary" className="text-xs">
                          built-in
                        </Badge>
                      )}
                    </div>
                    <Button
                      variant="ghost"
                      size="icon"
                      className="h-7 w-7 text-destructive hover:text-destructive"
                      onClick={() => {
                        revokeRole.mutate({
                          userId,
                          assignmentId: ur.id,
                        });
                      }}
                    >
                      <Trash2 className="h-3.5 w-3.5" />
                    </Button>
                  </div>
                ))}
              </div>
            )}
          </div>

          {/* Assign new role */}
          <div className="space-y-2">
            <h3 className="text-sm font-medium">Assign New Role</h3>
            <div className="flex items-center gap-2">
              <Select value={selectedRoleId} onValueChange={setSelectedRoleId}>
                <SelectTrigger className="flex-1">
                  <SelectValue placeholder="Select a role..." />
                </SelectTrigger>
                <SelectContent>
                  {allRoles?.map((role) => (
                    <SelectItem key={role.id} value={role.id}>
                      {role.name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
              <Button
                onClick={handleAssign}
                disabled={!selectedRoleId || assignRole.isPending}
                size="sm"
              >
                Assign
              </Button>
            </div>
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
