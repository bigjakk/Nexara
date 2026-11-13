import type { ReactNode } from "react";
import { AlertTriangle, Plus, Loader2 } from "lucide-react";
import { Panel } from "@/components/page/Panel";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Skeleton } from "@/components/ui/skeleton";
import { usePermissions } from "@/hooks/usePermissions";
import { cn } from "@/lib/utils";
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
      <Panel title="APT repositories">
        <Skeleton className="h-32 w-full" />
      </Panel>
    );
  }

  if (error) {
    return (
      <Panel title="APT repositories">
        <p className="text-sm text-destructive">Failed to load repositories</p>
      </Panel>
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
  const repositories = files.flatMap((f) => f.repositories);
  const enabledCount = repositories.filter((r) => r.Enabled === 1).length;
  const mutationError = toggleMutation.error ?? addMutation.error;
  const hasNotices =
    errors.length > 0 || warnings.length > 0 || mutationError != null;

  return (
    <>
      <Panel
        flush
        title="APT repositories"
        aside={
          repositories.length > 0
            ? `${String(enabledCount)} of ${String(repositories.length)} enabled, in ${String(files.length)} ${files.length === 1 ? "file" : "files"}`
            : undefined
        }
      >
        {hasNotices && (
          <div className="space-y-2 px-[18px] pb-3">
            {errors.map((e, i) => (
              <Notice key={`error-${String(i)}`} tone="bad">
                <span className="font-mono text-xs">{e.path}</span>: {e.error}
              </Notice>
            ))}
            {warnings.map((w, i) => (
              <Notice key={`warning-${String(i)}`} tone="warn">
                {w.message}
              </Notice>
            ))}
            {mutationError != null && (
              <Notice tone="bad">
                {mutationError.message || "Operation failed"}
              </Notice>
            )}
          </div>
        )}

        {files.length === 0 && (
          <p className="px-[18px] pb-1 text-sm text-muted-foreground">
            This node lists no repository files.
          </p>
        )}

        {files.map((file) => (
          <RepositoryFileSection
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
      </Panel>

      {/* Add standard repos */}
      {canEdit && unconfiguredStandard.length > 0 && (
        <Panel
          title="Available standard repositories"
          aside={`${String(unconfiguredStandard.length)} not configured`}
        >
          <div className="divide-y">
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
        </Panel>
      )}
    </>
  );
}

/** One sources file: its path as a quiet heading, then its repositories. */
function RepositoryFileSection({
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
    <section className="border-t">
      <h3 className="flex flex-wrap items-baseline gap-x-2 bg-foreground/[0.025] px-[18px] py-2 text-[12.5px] text-muted-foreground">
        <span className="font-mono [overflow-wrap:anywhere]">{file.path}</span>
        <span className="text-muted-foreground/75">{file["file-type"]}</span>
      </h3>
      <div className="divide-y border-t">
        {file.repositories.map((repo, index) => (
          <RepositoryRow
            key={index}
            repo={repo}
            filePath={file.path}
            index={index}
            info={infos.find((i) => i.path === file.path && i.index === index)}
            canEdit={canEdit}
            onToggle={onToggle}
            isToggling={isToggling}
          />
        ))}
        {file.repositories.length === 0 && (
          <p className="px-[18px] py-2.5 text-sm text-muted-foreground">
            No repositories in this file
          </p>
        )}
      </div>
    </section>
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
    <div className="flex items-start justify-between gap-4 px-[18px] py-2.5">
      <div
        className={cn(
          "min-w-0 flex-1 space-y-0.5",
          !enabled && "text-muted-foreground",
        )}
      >
        <div className="flex flex-wrap items-baseline gap-x-2 font-mono text-[13px]">
          <span className="text-muted-foreground">{types}</span>
          <span className="min-w-0 [overflow-wrap:anywhere]">{uris}</span>
        </div>
        <div className="flex flex-wrap gap-x-1.5 text-[12.5px] text-muted-foreground">
          <span>{suites}</span>
          {components && (
            <>
              <span aria-hidden="true" className="text-muted-foreground/50">
                ·
              </span>
              <span>{components}</span>
            </>
          )}
        </div>
        {repo.Comment && (
          <p className="text-[12.5px] text-muted-foreground">{repo.Comment}</p>
        )}
        {info && (
          <p className="text-[12.5px] text-muted-foreground">{info.message}</p>
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
        <span
          className={cn(
            "shrink-0 text-sm",
            !enabled && "text-muted-foreground",
          )}
        >
          {enabled ? "Enabled" : "Disabled"}
        </span>
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
    <div className="flex items-center justify-between gap-3 py-2.5 first:pt-0 last:pb-0">
      <div className="min-w-0 flex-1">
        <p className="text-sm font-medium">{repo.name}</p>
        <p className="text-[12.5px] text-muted-foreground">
          {repo.description}
        </p>
      </div>
      <Button
        size="sm"
        variant="outline"
        className="shrink-0 gap-1.5"
        onClick={onAdd}
        disabled={isAdding}
      >
        {isAdding ? (
          <Loader2 className="h-3.5 w-3.5 animate-spin" />
        ) : (
          <Plus className="h-3.5 w-3.5" />
        )}
        Add
      </Button>
    </div>
  );
}

/** Something wrong with the repositories, or with the last change to them. */
function Notice({
  tone,
  children,
}: {
  tone: "warn" | "bad";
  children: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex items-start gap-2 rounded-md border px-3 py-2 text-sm",
        tone === "bad"
          ? "border-destructive/40 bg-destructive/10 text-destructive"
          : "border-amber-500/40 bg-amber-500/10 text-amber-700 dark:text-amber-400",
      )}
    >
      <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
      <div className="min-w-0 [overflow-wrap:anywhere]">{children}</div>
    </div>
  );
}
