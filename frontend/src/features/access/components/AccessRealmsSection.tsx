import { Trans, useTranslation } from "react-i18next";
import { Panel } from "@/components/page/Panel";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";

import { useAccessDomains } from "../api/access-queries";

/**
 * Authentication realms, read-only.
 *
 * Creating or editing a realm requires the Realm.Allocate privilege, which
 * Proxmox keeps in its root privilege tier — no built-in role except
 * Administrator carries it, so the recommended PVEAdmin setup would fail. The
 * configuration surface is also large (LDAP/AD/OIDC), so this lists what exists
 * and points at the Proxmox UI for changes rather than half-implementing it.
 */
export function AccessRealmsSection({ clusterId }: { clusterId: string }) {
  const { t } = useTranslation("access");
  const domainsQuery = useAccessDomains(clusterId);
  const count = domainsQuery.data?.length ?? 0;

  return (
    <Panel
      flush
      title={t("realms.title")}
      aside={count === 0 ? undefined : t("realms.aside", { count })}
    >
      <p className="max-w-[75ch] px-[18px] pb-3 text-sm text-muted-foreground">
        <Trans
          t={t}
          i18nKey="realms.intro"
          components={{
            code: <code className="font-mono text-[12.5px]" />,
            em: <em />,
          }}
        />
      </p>

      {domainsQuery.isLoading ? (
        <div className="px-[18px] pb-1">
          <Skeleton className="h-20 w-full" />
        </div>
      ) : !domainsQuery.data || domainsQuery.data.length === 0 ? (
        <p className="px-[18px] pb-1 text-sm text-muted-foreground">
          {t("realms.none")}
        </p>
      ) : (
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("realms.realm")}</TableHead>
              <TableHead>{t("realms.type")}</TableHead>
              <TableHead>{t("realms.twoFactor")}</TableHead>
              <TableHead>{t("realms.comment")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {domainsQuery.data.map((domain) => (
              <TableRow key={domain.realm}>
                <TableCell className="font-medium">
                  <span className="flex items-center gap-2">
                    {domain.realm}
                    {domain.default && (
                      <span className="text-[12.5px] font-normal text-muted-foreground">
                        {t("realms.default")}
                      </span>
                    )}
                  </span>
                </TableCell>
                <TableCell className="font-mono text-[12.5px]">
                  {domain.type || "—"}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {domain.tfa || "—"}
                </TableCell>
                <TableCell className="text-muted-foreground">
                  {domain.comment || "—"}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      )}
    </Panel>
  );
}
