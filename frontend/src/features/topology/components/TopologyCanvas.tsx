import { useCallback, useEffect, useMemo } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  BackgroundVariant,
} from "@xyflow/react";
import type { Node, Edge } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useNavigate } from "react-router-dom";
import type { AggregatedMetrics } from "@/types/ws";
import { ClusterNode } from "./ClusterNode";
import { HostNode } from "./HostNode";
import { GuestNode } from "./GuestNode";
import { StorageNode } from "./StorageNode";
import { MigrationEdge } from "./MigrationEdge";
import type {
  TopologyNodeData,
  TopologyInput,
  TopologyFilters,
  HostNodeData,
} from "../lib/topology-transform";
import { buildTopologyGraph } from "../lib/topology-transform";
import { applyHierarchicalLayout } from "../lib/topology-layout";

const nodeTypes = {
  clusterNode: ClusterNode,
  hostNode: HostNode,
  guestNode: GuestNode,
  storageNode: StorageNode,
};

const edgeTypes = {
  migration: MigrationEdge,
};

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
}

function minimapColor(node: Node): string {
  const data = node.data as TopologyNodeData;
  switch (data.kind) {
    case "cluster":
      return "#10b981";
    case "host":
      return "#38bdf8";
    case "storage":
      return "#a78bfa";
    default:
      return "#6b7280";
  }
}

export function TopologyCanvas({
  input,
  filters,
  direction,
  liveMetrics,
  migrations,
}: TopologyCanvasProps) {
  const navigate = useNavigate();

  const { layoutNodes, layoutEdges } = useMemo(() => {
    const graph = buildTopologyGraph(input, filters);
    const positioned = applyHierarchicalLayout(graph.nodes, graph.edges, {
      direction,
    });
    return { layoutNodes: positioned, layoutEdges: graph.edges };
  }, [input, filters, direction]);

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

  const [nodes, setNodes, onNodesChange] = useNodesState(layoutNodes);

  // Edges are fully derived (layout + in-flight migrations) — no edit state.
  const flowEdges = useMemo(
    () => [...layoutEdges, ...migrationEdges],
    [layoutEdges, migrationEdges],
  );

  // Sync with new data when the graph actually changes, carrying over any
  // already-injected live metrics so rings don't flash back to empty.
  useEffect(() => {
    setNodes((current) => {
      const prevById = new Map(current.map((n) => [n.id, n]));
      return layoutNodes.map((node) => {
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
          },
        };
      });
    });
  }, [layoutNodes, setNodes]);

  // Inject live CPU/memory into host node data without disturbing positions
  // (users may have dragged nodes around).
  useEffect(() => {
    setNodes((current) =>
      current.map((node) => {
        if (node.type !== "hostNode") return node;
        const data = node.data as HostNodeData;
        const metric = liveMetrics
          .get(data.clusterId)
          ?.nodeMetrics.get(data.nodeId);
        if (!metric) return node;
        if (
          data.cpuPercent === metric.cpuPercent &&
          data.memPercent === metric.memPercent
        ) {
          return node;
        }
        return {
          ...node,
          data: {
            ...data,
            cpuPercent: metric.cpuPercent,
            memPercent: metric.memPercent,
          },
        };
      }),
    );
  }, [liveMetrics, setNodes]);

  const handleNodeClick = useCallback(
    (_event: React.MouseEvent, node: Node) => {
      const data = node.data as TopologyNodeData;
      switch (data.kind) {
        case "cluster":
          void navigate(`/clusters/${data.clusterId}`);
          break;
        case "host":
          void navigate(
            `/clusters/${data.clusterId}/nodes/${data.nodeId}`,
          );
          break;
        case "guest":
          void navigate(
            `/inventory/${data.type === "qemu" ? "vm" : "ct"}/${data.clusterId}/${data.vmId}`,
          );
          break;
        case "storage":
          void navigate(`/storage/${data.clusterId}/${data.storageId}`);
          break;
      }
    },
    [navigate],
  );

  return (
    <div className="h-full w-full overflow-hidden rounded-xl border bg-background/60">
      <ReactFlow
        nodes={nodes}
        edges={flowEdges}
        nodeTypes={nodeTypes}
        edgeTypes={edgeTypes}
        onNodesChange={onNodesChange}
        onNodeClick={handleNodeClick}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.1}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={22} size={1} />
        <Controls showInteractive={false} />
        <MiniMap
          pannable
          zoomable
          nodeColor={minimapColor}
          className="border! bg-card!"
        />
      </ReactFlow>
    </div>
  );
}
