import { http } from "@/utils/http";

export type AdminSession = {
  authenticated: boolean;
  setup_required: boolean;
};

export type NodeItem = {
  id: number;
  node_id: string;
  ssh_server: string;
  public_base_url: string;
  weight: number;
  enabled: boolean;
  maintenance: boolean;
  max_tunnels: number;
  current_tunnels: number;
  max_active_connections: number;
  active_connections: number;
  region: string;
  last_heartbeat: string;
  created_at: string;
  updated_at: string;
  healthy?: boolean;
  eligible?: boolean;
  score?: number;
  is_local?: boolean;
};

export type RouteItem = {
  id: number;
  token: string;
  node_id: string;
  target_url: string;
  public_url: string;
  peer_id: string;
  status: string;
  created_at: string;
  updated_at: string;
  expires_at: string;
};

export type AuditEvent = {
  actor: string;
  action: string;
  target: string;
  detail: Record<string, any>;
  created_at: string;
};

export type ClusterSetting = {
  key: string;
  value: string;
  updated_at: string;
};

export type Stats = {
  now: string;
  uptime: number;
  role: string;
  node_id: string;
  limits: {
    ssh: number;
    signal: number;
    viewers_per_peer: number;
  };
  ssh: {
    active: number;
    peers: number;
    total: number;
    rejected: number;
    replaced: number;
  };
  signal: {
    peers: number;
    viewers: number;
    total: number;
    rejected: number;
    messages_in: number;
    messages_out: number;
    bytes_in: number;
    bytes_out: number;
    viewer_total: number;
  };
  http: {
    p2p_pages: number;
    p2p_page_bytes: number;
  };
  admin: {
    logins: number;
    failed_logins: number;
  };
  cluster: {
    scheduler_total: number;
    scheduler_redirect: number;
    scheduler_local: number;
    scheduler_fail: number;
    route_register_total: number;
    route_register_fail: number;
    route_delete_total: number;
    route_redirect_total: number;
    route_lookup_miss: number;
    heartbeat_total: number;
    heartbeat_fail: number;
    nodes: NodeItem[];
    routes_active: number;
    routes_total: number;
  };
  peers: Array<{
    peer_id: string;
    ssh: boolean;
    signal: boolean;
    viewers: number;
    fallback_url: string;
    created_at: string;
    last_seen: string;
    ssh_connected_at: string;
    signal_connected_at: string;
    ssh_connections: number;
    signal_connections: number;
    viewers_total: number;
  }>;
};

export type Capacity = {
  nodes: number;
  healthy_nodes: number;
  eligible_nodes: number;
  current_tunnels: number;
  max_tunnels: number;
  active_connections: number;
  max_active_connections: number;
  unlimited_active_nodes: number;
  tunnel_utilization: number;
  active_connection_utilization: number;
};

type Result<T> = {
  ok?: boolean;
  authenticated?: boolean;
  setup_required?: boolean;
  node?: T;
  route?: T;
  nodes?: T[];
  routes?: T[];
  events?: T[];
  settings?: T[];
  data?: T;
  error?: string;
};

export const adminSession = () => {
  return http.request<AdminSession>("get", "/admin/api/session");
};

export const adminSetup = (password: string) => {
  return http.request<Result<never>>("post", "/admin/api/setup", {
    data: { password }
  });
};

export const adminLogin = (password: string) => {
  return http.request<Result<never>>("post", "/admin/api/login", {
    data: { password }
  });
};

export const adminLogout = () => {
  return http.request<Result<never>>("post", "/admin/api/logout", {
    data: {}
  });
};

export const adminStats = () => {
  return http.request<Stats>("get", "/admin/api/stats");
};

export const statsV1 = () => {
  return http.request<Stats>("get", "/api/v1/stats");
};

export const listNodes = () => {
  return http.request<{ nodes: NodeItem[] }>("get", "/api/v1/nodes");
};

export const patchNode = (nodeID: string, patch: Record<string, any>) => {
  return http.request<{ node: NodeItem }>("patch", `/api/v1/nodes/${encodeURIComponent(nodeID)}`, {
    data: patch
  });
};

export const deleteNode = (nodeID: string) => {
  return http.request<Result<never>>("delete", `/api/v1/nodes/${encodeURIComponent(nodeID)}`);
};

export const clusterCapacity = () => {
  return http.request<{ capacity: Capacity }>("get", "/api/v1/capacity");
};

export const patchNodeCapacity = (
  nodeID: string,
  patch: Pick<NodeItem, "max_tunnels" | "max_active_connections">
) => {
  return http.request<{ node: NodeItem }>(
    "patch",
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/capacity`,
    {
      data: patch
    }
  );
};

export const setNodeWeight = (nodeID: string, weight: number) => {
  return http.request<{ node: NodeItem }>(
    "patch",
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/weight`,
    {
      data: { weight }
    }
  );
};

export const setNodeMaintenance = (nodeID: string, maintenance: boolean) => {
  return http.request<{ node: NodeItem }>(
    "patch",
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/maintenance`,
    {
      data: { maintenance }
    }
  );
};

export const listRoutes = () => {
  return http.request<{ routes: RouteItem[] }>("get", "/api/v1/routes");
};

export const deleteRoute = (token: string) => {
  return http.request<Result<never>>("delete", `/api/v1/routes/${encodeURIComponent(token)}`);
};

export const listAuditEvents = () => {
  return http.request<{ events: AuditEvent[] }>("get", "/api/v1/audit-events");
};

export const listClusterSettings = () => {
  return http.request<{ settings: ClusterSetting[] }>("get", "/api/v1/settings");
};

export const upsertClusterSetting = (key: string, value: string) => {
  return http.request<Result<never>>("post", `/api/v1/settings/${encodeURIComponent(key)}`, {
    data: { value }
  });
};
