import { Fragment, useLayoutEffect, useRef } from "react";
import { Link } from "react-router-dom";
import { NativeSelect } from "@/components/page/Field";
import { cn } from "@/lib/utils";
import { ClusterGuestToolsTab } from "@/features/guest-tools/components/ClusterGuestToolsTab";
import {
  CONFIGURE_GROUPS,
  isConfigureSection,
  type ConfigureSection,
} from "../lib/configure-sections";
import { ClusterACMETab } from "./ClusterACMETab";
import { ClusterAccessTab } from "./ClusterAccessTab";
import { ClusterDRSTab } from "./ClusterDRSTab";
import { ClusterFirewallTab } from "./ClusterFirewallTab";
import { ClusterHATab } from "./ClusterHATab";
import { ClusterMetricServersTab } from "./ClusterMetricServersTab";
import { ClusterNetworksTab } from "./ClusterNetworksTab";
import { ClusterOptionsTab } from "./ClusterOptionsTab";
import { ClusterPoolsTab } from "./ClusterPoolsTab";
import { ClusterReplicationTab } from "./ClusterReplicationTab";
import { ClusterResourceMappingsTab } from "./ClusterResourceMappingsTab";

interface ClusterConfigureTabProps {
  clusterId: string;
  pveVersion: string;
  section: ConfigureSection;
  onSectionChange: (section: ConfigureSection) => void;
}

/**
 * The cluster's settings: the list of them in groups beside the one chosen,
 * or a select above it where the page is too narrow for the list. A setting
 * keeps its own sub-tabs, so the list is the only level this adds.
 */
export function ClusterConfigureTab({
  clusterId,
  pveVersion,
  section,
  onSectionChange,
}: ClusterConfigureTabProps) {
  const rootRef = useRef<HTMLDivElement>(null);
  const shownSection = useRef(section);
  // The list stays pinned while a long setting scrolls, so the next setting
  // can be picked from far down the last one: open it at its top instead.
  useLayoutEffect(() => {
    if (shownSection.current === section) return;
    shownSection.current = section;
    const root = rootRef.current;
    const scroller = root?.closest("main");
    if (!root || !scroller) return;
    if (
      root.getBoundingClientRect().top < scroller.getBoundingClientRect().top
    ) {
      root.scrollIntoView({ block: "start" });
    }
  }, [section]);

  return (
    // The scroll margin matches the shell's padding around a page.
    <div ref={rootRef} className="@container scroll-mt-4 md:scroll-mt-6">
      <div className="flex flex-col gap-4 @4xl:flex-row @4xl:items-start @4xl:gap-6">
        {/* The groups are parted by a rule, not titled: a title over every
            few settings read as one more setting. Each list still carries
            its group's name for a screen reader. */}
        <nav
          aria-label="Cluster settings"
          className="hidden w-[188px] shrink-0 divide-y @4xl:sticky @4xl:top-0 @4xl:block"
        >
          {CONFIGURE_GROUPS.map((group) => (
            <ul
              key={group.label}
              aria-label={group.label}
              className="space-y-0.5 py-2 first:pt-0 last:pb-0"
            >
              {group.items.map((item) => {
                const current = item.value === section;
                return (
                  <li key={item.value}>
                    <Link
                      to={`?tab=${item.value}`}
                      replace
                      aria-current={current ? "page" : undefined}
                      className={cn(
                        "block rounded-md px-2.5 py-1.5 text-sm transition-colors focus-visible:outline-hidden focus-visible:ring-2 focus-visible:ring-ring",
                        current
                          ? "bg-primary/10 font-medium text-foreground"
                          : "text-muted-foreground hover:bg-foreground/[0.06] hover:text-foreground",
                      )}
                    >
                      {item.label}
                    </Link>
                  </li>
                );
              })}
            </ul>
          ))}
        </nav>
        <NativeSelect
          aria-label="Cluster setting"
          className="@4xl:hidden"
          value={section}
          onChange={(e) => {
            if (isConfigureSection(e.target.value)) {
              onSectionChange(e.target.value);
            }
          }}
        >
          {/* The same rule between the groups, where the browser draws one. */}
          {CONFIGURE_GROUPS.map((group, index) => (
            <Fragment key={group.label}>
              {index > 0 && <hr />}
              {group.items.map((item) => (
                <option key={item.value} value={item.value}>
                  {item.label}
                </option>
              ))}
            </Fragment>
          ))}
        </NativeSelect>
        {/* Keyed on the setting: like a tab's content, what one holds (a
            dialog left open, a form half filled) does not carry to the next. */}
        <div key={section} className="min-w-0 flex-1">
          <SectionContent
            clusterId={clusterId}
            pveVersion={pveVersion}
            section={section}
          />
        </div>
      </div>
    </div>
  );
}

function SectionContent({
  clusterId,
  pveVersion,
  section,
}: {
  clusterId: string;
  pveVersion: string;
  section: ConfigureSection;
}) {
  switch (section) {
    case "options":
      return (
        <ClusterOptionsTab clusterId={clusterId} pveVersion={pveVersion} />
      );
    case "pools":
      return <ClusterPoolsTab clusterId={clusterId} />;
    case "mappings":
      return <ClusterResourceMappingsTab clusterId={clusterId} />;
    case "guest-tools":
      return <ClusterGuestToolsTab clusterId={clusterId} />;
    case "metric-servers":
      return <ClusterMetricServersTab clusterId={clusterId} />;
    case "ha":
      return <ClusterHATab clusterId={clusterId} pveVersion={pveVersion} />;
    case "drs":
      return <ClusterDRSTab clusterId={clusterId} />;
    case "replication":
      return <ClusterReplicationTab clusterId={clusterId} />;
    case "networks":
      return <ClusterNetworksTab clusterId={clusterId} />;
    case "firewall":
      return <ClusterFirewallTab clusterId={clusterId} />;
    case "access":
      return <ClusterAccessTab clusterId={clusterId} />;
    case "certificates":
      return <ClusterACMETab clusterId={clusterId} />;
  }
}
