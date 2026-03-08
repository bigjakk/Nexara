import { useCallback, useMemo } from "react";
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  useNodesState,
  useEdgesState,
  BackgroundVariant,
} from "@xyflow/react";
import type { Node } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { useNavigate } from "react-router-dom";
import { ClusterNode } from "./ClusterNode";
import { HostNode } from "./HostNode";
import { GuestNode } from "./GuestNode";
import { StorageNode } from "./StorageNode";
import type {
  TopologyNodeData,
  TopologyInput,
  TopologyFilters,
} from "../lib/topology-transform";
import { buildTopologyGraph } from "../lib/topology-transform";
import { applyHierarchicalLayout } from "../lib/topology-layout";

const nodeTypes = {
  clusterNode: ClusterNode,
  hostNode: HostNode,
  guestNode: GuestNode,
  storageNode: StorageNode,
};

interface TopologyCanvasProps {
  input: TopologyInput;
  filters: TopologyFilters;
  direction: "TB" | "LR";
}

export function TopologyCanvas({
  input,
  filters,
  direction,
}: TopologyCanvasProps) {
  const navigate = useNavigate();

  const { layoutNodes, layoutEdges } = useMemo(() => {
    const graph = buildTopologyGraph(input, filters);
    const positioned = applyHierarchicalLayout(graph.nodes, graph.edges, {
      direction,
    });
    return { layoutNodes: positioned, layoutEdges: graph.edges };
  }, [input, filters, direction]);

  const [nodes, setNodes, onNodesChange] = useNodesState(layoutNodes);
  const [edges, setEdges, onEdgesChange] = useEdgesState(layoutEdges);

  // Sync with new data when it changes
  useMemo(() => {
    setNodes(layoutNodes);
    setEdges(layoutEdges);
  }, [layoutNodes, layoutEdges, setNodes, setEdges]);

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
      }
    },
    [navigate],
  );

  return (
    <div className="h-full w-full rounded-lg border bg-background">
      <ReactFlow
        nodes={nodes}
        edges={edges}
        nodeTypes={nodeTypes}
        onNodesChange={onNodesChange}
        onEdgesChange={onEdgesChange}
        onNodeClick={handleNodeClick}
        fitView
        fitViewOptions={{ padding: 0.2 }}
        minZoom={0.1}
        maxZoom={2}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={16} size={1} />
        <Controls showInteractive={false} />
        <MiniMap
          pannable
          zoomable
          className="!bg-card !border"
        />
      </ReactFlow>
    </div>
  );
}
