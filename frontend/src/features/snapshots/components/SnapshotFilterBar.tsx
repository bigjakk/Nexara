import { useTranslation } from "react-i18next";
import { Input } from "@/components/ui/input";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";

export interface SnapshotFilters {
  search: string;
  clusterId: string; // "" = all
  guestType: string; // "" | "qemu" | "lxc"
  age: string; // "" | "over7" | "over30" | "unknown"
}

interface SnapshotFilterBarProps {
  filters: SnapshotFilters;
  onChange: (filters: SnapshotFilters) => void;
  clusterOptions: { id: string; name: string }[];
}

const ALL = "__all__";

export function SnapshotFilterBar({
  filters,
  onChange,
  clusterOptions,
}: SnapshotFilterBarProps) {
  const { t } = useTranslation("snapshots");

  return (
    <div className="flex flex-wrap items-center gap-2">
      <Input
        className="h-8 w-full sm:w-56"
        placeholder={t("filters.search")}
        aria-label={t("filters.search")}
        value={filters.search}
        onChange={(e) => {
          onChange({ ...filters, search: e.target.value });
        }}
      />
      <Select
        value={filters.clusterId || ALL}
        onValueChange={(v) => {
          onChange({ ...filters, clusterId: v === ALL ? "" : v });
        }}
      >
        <SelectTrigger className="h-8 w-40" aria-label={t("table.cluster")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>{t("filters.allClusters")}</SelectItem>
          {clusterOptions.map((c) => (
            <SelectItem key={c.id} value={c.id}>
              {c.name}
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <Select
        value={filters.guestType || ALL}
        onValueChange={(v) => {
          onChange({ ...filters, guestType: v === ALL ? "" : v });
        }}
      >
        <SelectTrigger className="h-8 w-32" aria-label={t("filters.type")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>{t("filters.allTypes")}</SelectItem>
          <SelectItem value="qemu">{t("filters.vms")}</SelectItem>
          <SelectItem value="lxc">{t("filters.containers")}</SelectItem>
        </SelectContent>
      </Select>
      <Select
        value={filters.age || ALL}
        onValueChange={(v) => {
          onChange({ ...filters, age: v === ALL ? "" : v });
        }}
      >
        <SelectTrigger className="h-8 w-44" aria-label={t("table.age")}>
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value={ALL}>{t("filters.allAges")}</SelectItem>
          <SelectItem value="over7">{t("filters.olderThan7")}</SelectItem>
          <SelectItem value="over30">{t("filters.olderThan30")}</SelectItem>
          <SelectItem value="unknown">{t("filters.unknownAge")}</SelectItem>
        </SelectContent>
      </Select>
    </div>
  );
}
