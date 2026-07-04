export interface HealthStatus {
  status: string;
  expires_at?: string;
  error_count: number;
  cooldown_remaining?: string;
}

export interface ToolStatus {
  tool: string;
  logged_in: boolean;
  active_profile?: string;
  health?: HealthStatus;
}

export interface StatusResponse {
  version: string;
  timestamp: string;
  tools: ToolStatus[];
}

export interface ProfileInfo {
  tool: string;
  name: string;
  active: boolean;
  system: boolean;
  health?: HealthStatus;
}

export interface ProfilesResponse {
  profiles: ProfileInfo[];
  count: number;
}

export interface UsageEntry {
  tool: string;
  profile: string;
  health_error_count_1h: number;
  last_checked?: string;
}

export interface UsageResponse {
  tool?: string;
  window: string;
  metric: "health_error_count";
  entries: UsageEntry[];
}

export interface CoordinatorStatus {
  id: string;
  endpoint: string;
  status: string;
  backend?: string;
  last_seen?: string;
  pane_count?: number;
  pending_auths?: number;
  error?: string;
}

export interface CoordinatorsResponse {
  coordinators: CoordinatorStatus[];
}

export interface ActivityEntry {
  timestamp: string;
  type: string;
  tool: string;
  profile: string;
  message: string;
  duration_seconds?: number;
}

export interface ActivityResponse {
  events: ActivityEntry[];
  count: number;
}

export interface DashboardSnapshot {
  status: StatusResponse;
  profiles: ProfilesResponse;
  usage: UsageResponse;
  coordinators: CoordinatorsResponse;
  activity: ActivityResponse;
}

export const dashboardAuthStorageKey = "caam.dashboard.auth";

export function defaultApiBaseUrl() {
  return (
    process.env.NEXT_PUBLIC_CAAM_API_BASE_URL?.replace(/\/+$/, "") ??
    "http://127.0.0.1:7891"
  );
}

export function getStoredApiToken() {
  if (typeof window === "undefined") {
    return "";
  }
  try {
    return window.sessionStorage?.getItem(dashboardAuthStorageKey) ?? "";
  } catch {
    return "";
  }
}

export function storeApiToken(token: string) {
  if (typeof window === "undefined") {
    return;
  }
  const trimmed = token.trim();
  try {
    if (trimmed === "") {
      window.sessionStorage?.removeItem(dashboardAuthStorageKey);
      return;
    }
    window.sessionStorage?.setItem(dashboardAuthStorageKey, trimmed);
  } catch {
    return;
  }
}

async function fetchJSON<T>(baseUrl: string, token: string, path: string) {
  const response = await fetch(`${baseUrl}${path}`, {
    headers: {
      Authorization: `Bearer ${token}`,
    },
    signal: requestTimeoutSignal(),
  });
  if (!response.ok) {
    if (response.status === 401) {
      throw new Error("API rejected the bearer token");
    }
    throw new Error(`API request failed: ${response.status}`);
  }
  return (await response.json()) as T;
}

function requestTimeoutSignal() {
  if (typeof AbortSignal === "undefined" || !("timeout" in AbortSignal)) {
    return undefined;
  }
  return AbortSignal.timeout(15_000);
}

export async function fetchDashboardSnapshot(
  token: string,
  baseUrl = defaultApiBaseUrl(),
): Promise<DashboardSnapshot> {
  const trimmed = token.trim();
  if (trimmed === "") {
    throw new Error("API token is required");
  }

  const [status, profiles, usage, coordinators, activity] = await Promise.all([
    fetchJSON<StatusResponse>(baseUrl, trimmed, "/api/v1/status"),
    fetchJSON<ProfilesResponse>(baseUrl, trimmed, "/api/v1/profiles"),
    fetchJSON<UsageResponse>(baseUrl, trimmed, "/api/v1/usage"),
    fetchJSON<CoordinatorsResponse>(baseUrl, trimmed, "/api/v1/coordinators"),
    fetchJSON<ActivityResponse>(baseUrl, trimmed, "/api/v1/activity"),
  ]);

  return { status, profiles, usage, coordinators, activity };
}
