import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { RotateCcw } from "lucide-react";
import {
  ReactFlow,
  Background,
  ControlButton,
  Controls,
  MiniMap,
  getNodesBounds,
  useNodesState,
  useReactFlow,
  useStore,
  BackgroundVariant,
} from "@xyflow/react";
import type { Node, Edge, NodeChange, XYPosition } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useNavigate } from "react-router-dom";
import type { AggregatedMetrics } from "@/types/ws";
import { ClusterNode } from "./ClusterNode";
import { HostNode } from "./HostNode";
import { GuestNode } from "./GuestNode";
import { StorageNode } from "./StorageNode";
import { MigrationEdge } from "./MigrationEdge";
import { StackEdge } from "./StackEdge";
import { UpsNode } from "./UpsNode";
import type { UpsDevice } from "@/features/power/types/ups";
import type {
  TopologyNodeData,
  TopologyInput,
  TopologyFilters,
  HostNodeData,
} from "../lib/topology-transform";
import { buildTopologyGraph } from "../lib/topology-transform";
import {
  applyHierarchicalLayout,
  withStackEdges,
} from "../lib/topology-layout";
import { buildPowerOverlay, withPower } from "../lib/topology-power";
import {
  arranged,
  noteDrags,
  type Arrangement,
} from "../lib/topology-arrangement";

const nodeTypes = {
  clusterNode: ClusterNode,
  hostNode: HostNode,
  guestNode: GuestNode,
  storageNode: StorageNode,
  upsNode: UpsNode,
};

const edgeTypes = {
  migration: MigrationEdge,
  stack: StackEdge,
};

/**
 * Room for what floats over the canvas: the options along the top, the legend
 * along the bottom, the zoom buttons down the right. The graph is fitted
 * inside it.
 */
const FIT_OPTIONS = {
  padding: { top: "72px", bottom: "64px", left: "28px", right: "64px" },
  maxZoom: 1.1,
} as const;

/**
 * Fits the graph to the canvas again whenever its shape changes (a filter, the
 * direction, the power overlay). Not on a refetch or a metric tick: those
 * must not take the view away from wherever the viewer has panned.
 *
 * It waits until the canvas holds the new layout and has measured every node
 * of it. This component's effects run before its parent has handed the canvas
 * the new nodes, so fitting on the change itself would frame the old ones.
 */
function FitOnChange({
  shape,
  layout,
  arrangement,
  count,
}: {
  /** What the graph is made of; a new value asks for a new fit. */
  shape: string;
  layout: Node[];
  /** Where the person has dragged nodes: those are held there, not laid out. */
  arrangement: ReadonlyMap<string, XYPosition>;
  /** How many nodes the canvas holds once the overlay is on it too. */
  count: number;
}) {
  const { fitView } = useReactFlow();
  const [fitted, setFitted] = useState<string | null>(null);
  const pending = fitted !== shape;
  const applied = useStore(
    (s) =>
      pending &&
      s.nodes.length === count &&
      s.nodes.every((n) => n.measured?.width !== undefined) &&
      layout.every((n) => {
        const held = s.nodeLookup.get(n.id)?.position;
        const want = arrangement.get(n.id) ?? n.position;
        return held?.x === want.x && held.y === want.y;
      }),
  );
  useEffect(() => {
    if (!pending || !applied) return;
    setFitted(shape);
    void fitView({ ...FIT_OPTIONS, duration: fitted === null ? 0 : 250 });
  }, [pending, applied, shape, fitted, fitView]);
  return null;
}

/** A running migrate task rendered as an in-flight edge between two hosts. */
export interface MigrationFlight {
  id: string;
  clusterId: string;
  vmid: number;
  name: string;
  sourceName: string;
  targetName: string;
}

interface TopologyCanvasProps {
  input: TopologyInput;
  filters: TopologyFilters;
  direction: "TB" | "LR";
  liveMetrics: Map<string, AggregatedMetrics>;
  migrations: MigrationFlight[];
  /** The UPSes for the power overlay, or null while it is off. */
  power: UpsDevice[] | null;
}

/** The minimap is a shape to steer by, not a second legend: all neutral. */
function minimapColor(node: Node): string {
  return (node.data as TopologyNodeData).kind === "cluster"
    ? "hsl(var(--foreground) / 0.55)"
    : "hsl(var(--foreground) / 0.25)";
}

/**
 * The minimap, only while part of the map is out of view: fitted, the canvas
 * already shows all of it and a second, smaller copy would only cover a
 * corner of the first.
 */
function MiniMapWhenOutOfView() {
  const outOfView = useStore((s) => {
    if (s.nodes.length === 0 || s.width === 0) return false;
    const bounds = getNodesBounds(s.nodes);
    const [x, y, zoom] = s.transform;
    return (
      bounds.x * zoom + x < -1 ||
      bounds.y * zoom + y < -1 ||
      (bounds.x + bounds.width) * zoom + x > s.width + 1 ||
      (bounds.y + bounds.height) * zoom + y > s.height + 1
    );
  });
  if (!outOfView) return null;
  return (
    <MiniMap
      pannable
      zoomable
      nodeColor={minimapColor}
      nodeStrokeWidth={0}
      className="m-3! bg-card! ring-1 ring-border max-md:hidden!"
    />
  );
}

export function TopologyCanvas({
  input,
  filters,
  direction,
  liveMetrics,
  migrations,
  power,
}: TopologyCanvasProps) {
  const navigate = useNavigate();
  const { t } = useTranslation("topology");

  // The layout leaves room for the UPS lanes only while the overlay is on,
  // so turning it on or off re-lays the map out; a UPS poll does not.
  const reserveLane = power !== null;
  const { layoutNodes, layoutEdges } = useMemo(() => {
    const graph = buildTopologyGraph(input, filters);
    const positioned = applyHierarchicalLayout(graph.nodes, graph.edges, {
      direction,
      reserveLane,
    });
    return {
      layoutNodes: positioned,
      layoutEdges:
        direction === "TB"
          ? withStackEdges(positioned, graph.edges)
          : graph.edges,
    };
  }, [input, filters, direction, reserveLane]);

  // Resolve "<clusterId>:<nodeName>" -> host flow-node id for migration edges.
  const hostIdByName = useMemo(() => {
    const map = new Map<string, string>();
    for (const [clusterId, nodes] of input.nodesByCluster) {
      for (const n of nodes) {
        map.set(`${clusterId}:${n.name}`, `host-${n.id}`);
      }
    }
    return map;
  }, [input.nodesByCluster]);

  const migrationEdges = useMemo<Edge[]>(() => {
    const out: Edge[] = [];
    for (const m of migrations) {
      const source = hostIdByName.get(`${m.clusterId}:${m.sourceName}`);
      const target = hostIdByName.get(`${m.clusterId}:${m.targetName}`);
      if (!source || !target || source === target) continue;
      out.push({
        id: `migration-${m.id}`,
        source,
        target,
        type: "migration",
        zIndex: 10,
        data: { label: `${String(m.vmid)} ${m.name} → ${m.targetName}` },
      });
    }
    return out;
  }, [migrations, hostIdByName]);

  // The power overlay sits outside the layout, as migrations do, so a UPS
  // never becomes a host's parent.
  const onCanvas = useMemo(
    () =>
      new Set(
        layoutNodes.filter((n) => n.type === "hostNode").map((n) => n.id),
      ),
    [layoutNodes],
  );
  const overlay = useMemo(
    () =>
      power === null
        ? null
        : buildPowerOverlay(power, hostIdByName, onCanvas, direction),
    [power, hostIdByName, onCanvas, direction],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState(layoutNodes);

  // What the person arranged by hand, held over every re-layout (a refetch
  // lays the graph out again) until they reset it or pick another direction
  // or overlay, which lay it out anew. A ref: a drag notes a position on
  // every move, and nothing renders from it but what setNodes is given.
  const arrangement = useRef<Arrangement>(new Map());
  const [isArranged, setIsArranged] = useState(false);
  const [resets, setResets] = useState(0);
  const basis = `${direction}|${String(reserveLane)}|${String(resets)}`;
  const arrangedFor = useRef(basis);
  const handleNodesChange = useCallback(
    (changes: NodeChange[]) => {
      if (noteDrags(arrangement.current, changes)) setIsArranged(true);
      onNodesChange(changes);
    },
    [onNodesChange],
  );

  // Edges are fully derived (layout + in-flight migrations + power) — no
  // edit state.
  const flowEdges = useMemo(
    () => [...layoutEdges, ...migrationEdges, ...(overlay?.edges ?? [])],
    [layoutEdges, migrationEdges, overlay],
  );

  // Sync with new data when the graph actually changes, carrying over any
  // already-injected live metrics so rings don't flash back to empty, and the
  // places the person dragged nodes to.
  useEffect(() => {
    if (arrangedFor.current !== basis) {
      arrangedFor.current = basis;
      arrangement.current.clear();
      setIsArranged(false);
    }
    setNodes((current) => {
      const prevById = new Map(current.map((n) => [n.id, n]));
      const synced = layoutNodes.map((node) => {
        if (node.type !== "hostNode") return node;
        const prev = prevById.get(node.id);
        if (!prev) return node;
        const prevData = prev.data as HostNodeData;
        if (prevData.cpuPercent === undefined) return node;
        return {
          ...node,
          data: {
            ...node.data,
            cpuPercent: prevData.cpuPercent,
            memPercent: prevData.memPercent,
            guestCpu: prevData.guestCpu,
          },
        };
      });
      return arranged(synced, arrangement.current);
    });
  }, [layoutNodes, setNodes, basis]);

  // After the sync above, which drops the overlay with every re-layout (the
  // overlay follows layoutNodes, so this runs again): applied to the nodes as
  // live metrics are, so a UPS poll never moves a dragged node.
  useEffect(() => {
    setNodes((current) =>
      arranged(withPower(current, overlay, direction), arrangement.current),
    );
  }, [overlay, direction, setNodes]);

  // Inject live CPU/memory, and each running guest's CPU for its cell, into
  // host node data without disturbing positions (users may have dragged nodes
  // around).
  useEffect(() => {
    setNodes((current) =>
      current.map((node) => {
        if (node.type !== "hostNode") return node;
        const data = node.data as HostNodeData;
        const cluster = liveMetrics.get(data.clusterId);
        const metric = cluster?.nodeMetrics.get(data.nodeId);
        if (!metric) return node;
        const guestCpu: Record<string, number> = {};
        for (const g of data.guests) {
          const cpu = cluster?.vmMetrics.get(g.vmId)?.cpuPercent;
          // Whole percents: what a cell shows, and steady between ticks.
          if (cpu !== undefined) guestCpu[g.vmId] = Math.round(cpu);
        }
        if (
          data.cpuPercent === metric.cpuPercent &&
          data.memPercent === metric.memPercent &&
          sameReadings(data.guestCpu, guestCpu)
        ) {
          return node;
        }
        return {
          ...node,
          data: {
            ...data,
            cpuPercent: metric.cpuPercent,
            memPercent: metric.memPercent,
            guestCpu,
          },
        };
      }),
    );
  }, [liveMetrics, setNodes]);

  // What the graph is made of, as opposed to what it currently reads.
  const shape = useMemo(
    () =>
      [
        direction,
        String(overlay?.ups.length ?? -1),
        ...layoutNodes.map((n) => n.id),
      ].join("|"),
    [direction, overlay, layoutNodes],
  );

  const handleNodeClick = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      const data = node.data as TopologyNodeData;
      switch (data.kind) {
        case "cluster":
          void navigate(`/clusters/${data.clusterId}`);
          break;
        case "host":
          void navigate(`/clusters/${data.clusterId}/nodes/${data.nodeId}`);
          break;
        case "guest":
          void navigate(
            `/inventory/${data.type === "qemu" ? "vm" : "ct"}/${data.clusterId}/${data.vmId}`,
          );
          break;
        case "storage":
          void navigate(`/storage/${data.clusterId}/${data.storageId}`);
          break;
        case "ups":
          void navigate(`/power/${data.deviceId}`);
          break;
      }
    },
    [navigate],
  );

  return (
    // No box: the graph sits on the page's own canvas, as its panels do.
    <div className="h-full w-full">
      <ReactFlow
        nodes={nodes}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={handleNodesChange}
        onNodeClick={handleNodeClick}
        nodesConnectable={false}
        fitView
        fitViewOptions={FIT_OPTIONS}
        minZoom={0.1}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <FitOnChange
          shape={shape}
          layout={layoutNodes}
          arrangement={arrangement.current}
          count={layoutNodes.length + (overlay?.ups.length ?? 0)}
        />
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} />
        <Controls
          showInteractive={false}
          fitViewOptions={FIT_OPTIONS}
          position="top-right"
          className="m-3! shadow-none! ring-1 ring-border"
        >
          {isArranged && (
            <ControlButton
              onClick={() => {
                setResets((n) => n + 1);
              }}
              title={t("resetLayout")}
              aria-label={t("resetLayout")}
            >
              <RotateCcw />
            </ControlButton>
          )}
        </Controls>
        <MiniMapWhenOutOfView />
      </ReactFlow>
    </div>
  );
}

/** Whether two guest CPU maps hold the same readings. */
function sameReadings(
  a: Record<string, number> | undefined,
  b: Record<string, number>,
): boolean {
  if (a === undefined) return false;
  const keys = Object.keys(b);
  return (
    keys.length === Object.keys(a).length && keys.every((k) => a[k] === b[k])
  );
}
