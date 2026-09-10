import type { AptPackage } from "@/types/api";

/**
 * How many of these pending updates are security updates.
 *
 * Shared rather than repeated per panel: this is the rule that decides whether
 * an operator sees a red badge and goes and patches a node tonight, and it was
 * already written out twice — once in the cluster-wide overview and once on the
 * node's own tab. Two copies of a threshold rule drift, and the drift is
 * invisible until the two panels disagree about the same node.
 *
 * Debian marks security updates in two ways and neither alone is enough:
 * `Origin` names the security archive, while `Priority: important` catches the
 * ones pushed through the ordinary archive.
 */
export function securityPackageCount(packages: AptPackage[] | undefined) {
  return (
    packages?.filter(
      (p) => p.Priority === "important" || p.Origin === "Debian-Security",
    ).length ?? 0
  );
}
