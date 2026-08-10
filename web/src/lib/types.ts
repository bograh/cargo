export interface User {
  id: string;
  email: string;
  is_instance_admin: boolean;
}

export interface Org {
  id: string;
  name: string;
  slug: string;
  created_at?: string;
  role?: string;
}

export interface Member {
  UserID: string;
  Role: string;
  Email: string;
}

export interface Invite {
  ID: string;
  Role: string;
  Email?: { String: string; Valid: boolean } | string;
  ExpiresAt: { Time: string; Valid: boolean } | string;
  CreatedAt?: unknown;
}

export interface App {
  id: string;
  org_id: string;
  name: string;
  slug: string;
  source_type: "git" | "image";
  builder: string;
  git_repo_url: string;
  git_branch: string;
  image_ref: string;
  exposed_port: number;
  healthcheck_path: string;
  auto_deploy: boolean;
  build_context: string;
  dockerfile_path: string;
  has_registry_credentials: boolean;
  desired_state: "running" | "stopped";
  mem_limit: string | null;
  cpu_limit: string | null;
  pids_limit: number | null;
  notify_on_success: boolean;
  deploy_strategy: "bluegreen" | "recreate" | null;
  created_at?: unknown;
  updated_at?: unknown;
}

export interface Deployment {
  id: string;
  app_id: string;
  trigger: "manual" | "webhook" | "rollback";
  status: "queued" | "building" | "deploying" | "live" | "superseded" | "failed" | "cancelled";
  commit_sha: string;
  image_tag: string;
  error: string;
  created_at?: unknown;
  started_at?: unknown;
  finished_at?: unknown;
}

export interface DatabaseInstance {
  id: string;
  org_id: string;
  name: string;
  engine: "postgres" | "redis" | "mysql" | "mongodb";
  version: string;
  redis_mode: string;
  host_port: number | null;
  status: "provisioning" | "running" | "error" | "stopped" | string;
  created_at?: unknown;
  attachment_count: number;
}

export interface DatabaseAttachment {
  id: string;
  app_id: string;
  db_name?: string;
  db_index?: number;
  created_at?: unknown;
}

export interface DatabaseDetail extends DatabaseInstance {
  size_bytes: number;
  attachments: DatabaseAttachment[];
}

export interface Snapshot {
  name: string;
  size: number;
  created_at: string;
}

export interface AppMetric {
  ts: string;
  cpu_pct: number;
  mem_bytes: number;
  mem_limit_bytes: number;
  net_rx_bytes: number;
  net_tx_bytes: number;
  req_rate: number;
  err_rate: number;
  p50_ms: number;
  p95_ms: number;
}

export const ACTIVE_STATUSES = ["queued", "building", "deploying"] as const;

export function isActive(status: Deployment["status"]): boolean {
  return (ACTIVE_STATUSES as readonly string[]).includes(status);
}
