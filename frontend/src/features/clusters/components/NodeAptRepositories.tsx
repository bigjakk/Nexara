import { Package, AlertTriangle, Plus, Loader2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { usePermissions } from "@/hooks/usePermissions";
import {
  useNodeAptRepositories,
  useToggleAptRepository,
  useAddStandardAptRepository,
} from "../api/apt-repository-queries";
import type {
  AptRepositoryFile,
  AptRepository,
  AptStandardRepo,
  AptRepositoryInfo,
} from "@/types/api";

interface Props {
  clusterId: string;
  nodeName: string;
}

export function NodeAptRepositories({ clusterId, nodeName }: Props) {
  const { data, isLoading, error } = useNodeAptRepositories(
    clusterId,
    nodeName,
  );
  const toggleMutation = useToggleAptRepository(clusterId, nodeName);
  const addMutation = useAddStandardAptRepository(clusterId, nodeName);
  const { canManage } = usePermissions();
  const canEdit = canManage("apt_repository");

  if (isLoading) {
    return (
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <Package className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold">APT Repositories</h2>
        </div>
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-3">
        <div className="flex items-center gap-2">
          <Package className="h-4 w-4 text-muted-foreground" />
          <h2 className="text-lg font-semibold">APT Repositories</h2>
        </div>
        <p className="text-sm text-destructive">
          Failed to load repositories
        </p>
      </div>
    );
  }

  if (!data) return null;

  const digest = data.digest;
  const files = data.files ?? [];
  const infos = data.infos ?? [];
  const standardRepos = data["standard-repos"] ?? [];
  const errors = data.errors ?? [];
  const warnings = infos.filter((i) => i.kind === "warning");
  const unconfiguredStandard = standardRepos.filter((r) => r.status === 0);

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2">
        <Package className="h-4 w-4 text-muted-foreground" />
        <h2 className="text-lg font-semibold">APT Repositories</h2>
      </div>

      {/* Errors */}
      {errors.length > 0 && (
        <div className="space-y-2">
          {errors.map((e, i) => (
            <ErrorBanner key={i} path={e.path} message={e.error} />
          ))}
        </div>
      )}

      {/* Warnings */}
      {warnings.length > 0 && (
        <div className="space-y-2">
          {warnings.map((w, i) => (
            <WarningBanner key={i} message={w.message} />
          ))}
        </div>
      )}

      {/* Mutation error */}
      {(toggleMutation.error ?? addMutation.error) != null && (
        <div className="rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {(toggleMutation.error ?? addMutation.error)?.message ??
            "Operation failed"}
        </div>
      )}

      {/* Repository files */}
      {files.map((file) => (
        <RepositoryFileCard
          key={file.path}
          file={file}
          infos={infos}
          canEdit={canEdit}
          onToggle={(path, index, enabled) => {
            toggleMutation.mutate({ path, index, enabled, digest });
          }}
          isToggling={toggleMutation.isPending}
        />
      ))}

      {/* Add standard repos */}
      {canEdit && unconfiguredStandard.length > 0 && (
        <div className="rounded-lg border p-4">
          <h3 className="mb-3 text-sm font-medium">
            Available Standard Repositories
          </h3>
          <div className="space-y-2">
            {unconfiguredStandard.map((repo) => (
              <StandardRepoRow
                key={repo.handle}
                repo={repo}
                onAdd={() => {
                  addMutation.mutate({ handle: repo.handle, digest });
                }}
                isAdding={addMutation.isPending}
              />
            ))}
          </div>
        </div>
      )}
    </div>
  );
}

function RepositoryFileCard({
  file,
  infos,
  canEdit,
  onToggle,
  isToggling,
}: {
  file: AptRepositoryFile;
  infos: AptRepositoryInfo[];
  canEdit: boolean;
  onToggle: (path: string, index: number, enabled: boolean) => void;
  isToggling: boolean;
}) {
  return (
    <div className="rounded-lg border">
      <div className="border-b bg-muted/50 px-3 py-2">
        <span className="font-mono text-xs text-muted-foreground">
          {file.path}
        </span>
        <Badge variant="outline" className="ml-2 text-xs">
          {file["file-type"]}
        </Badge>
      </div>
      <div className="divide-y">
        {file.repositories.map((repo, index) => (
          <RepositoryRow
            key={index}
            repo={repo}
            filePath={file.path}
            index={index}
            info={infos.find(
              (i) => i.path === file.path && i.index === index,
            )}
            canEdit={canEdit}
            onToggle={onToggle}
            isToggling={isToggling}
          />
        ))}
        {file.repositories.length === 0 && (
          <p className="px-3 py-2 text-sm text-muted-foreground">
            No repositories in this file
          </p>
        )}
      </div>
    </div>
  );
}

function RepositoryRow({
  repo,
  filePath,
  index,
  info,
  canEdit,
  onToggle,
  isToggling,
}: {
  repo: AptRepository;
  filePath: string;
  index: number;
  info: AptRepositoryInfo | undefined;
  canEdit: boolean;
  onToggle: (path: string, index: number, enabled: boolean) => void;
  isToggling: boolean;
}) {
  const enabled = repo.Enabled === 1;
  const types = (repo.Types ?? []).join(", ");
  const uris = (repo.URIs ?? []).join(" ");
  const suites = (repo.Suites ?? []).join(" ");
  const components = (repo.Components ?? []).join(" ");

  return (
    <div
      className={`flex items-start justify-between gap-4 px-3 py-2.5 ${
        !enabled ? "bg-muted/30" : ""
      }`}
    >
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-1.5">
          <Badge
            variant={enabled ? "default" : "secondary"}
            className="text-xs"
          >
            {types}
          </Badge>
          <span className="truncate font-mono text-xs">{uris}</span>
        </div>
        <div className="flex flex-wrap gap-1 text-xs text-muted-foreground">
          <span>{suites}</span>
          {components && (
            <>
              <span className="text-muted-foreground/50">|</span>
              <span>{components}</span>
            </>
          )}
        </div>
        {repo.Comment && (
          <p className="text-xs italic text-muted-foreground">
            {repo.Comment}
          </p>
        )}
        {info && (
          <p className="text-xs text-muted-foreground">{info.message}</p>
        )}
      </div>
      {canEdit ? (
        <Switch
          checked={enabled}
          disabled={isToggling}
          onCheckedChange={(checked: boolean) => {
            onToggle(filePath, index, checked);
          }}
          aria-label={`${enabled ? "Disable" : "Enable"} repository`}
        />
      ) : (
        <Badge variant={enabled ? "default" : "secondary"} className="text-xs">
          {enabled ? "Enabled" : "Disabled"}
        </Badge>
      )}
    </div>
  );
}

function StandardRepoRow({
  repo,
  onAdd,
  isAdding,
}: {
  repo: AptStandardRepo;
  onAdd: () => void;
  isAdding: boolean;
}) {
  return (
    <div className="flex items-center justify-between gap-3 rounded-md border px-3 py-2">
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{repo.name}</p>
        <p className="truncate text-xs text-muted-foreground">
          {repo.description}
        </p>
      </div>
      <Button
        size="sm"
        variant="outline"
        className="shrink-0 gap-1"
        onClick={onAdd}
        disabled={isAdding}
      >
        {isAdding ? (
          <Loader2 className="h-3 w-3 animate-spin" />
        ) : (
          <Plus className="h-3 w-3" />
        )}
        Add
      </Button>
    </div>
  );
}

function ErrorBanner({ path, message }: { path: string; message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-destructive/50 bg-destructive/10 px-3 py-2 text-sm text-destructive">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div>
        <span className="font-mono text-xs">{path}</span>: {message}
      </div>
    </div>
  );
}

function WarningBanner({ message }: { message: string }) {
  return (
    <div className="flex items-start gap-2 rounded-md border border-yellow-500/50 bg-yellow-500/10 px-3 py-2 text-sm text-yellow-700 dark:text-yellow-400">
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <span>{message}</span>
    </div>
  );
}
