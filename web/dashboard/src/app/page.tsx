"use client";

import { motion } from "framer-motion";
import {
  Activity,
  ArrowUpRight,
  Users,
  Key,
  AlertCircle,
  CheckCircle2,
  Copy,
  RefreshCw,
  Settings,
} from "lucide-react";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useMemo, useState } from "react";
import {
  Badge,
  Button,
  Card,
  CardContent,
  CardHeader,
  CardTitle,
  DashboardLayout,
} from "@/components";
import {
  type ActivityEntry,
  type DashboardSnapshot,
  type ProfileInfo,
  defaultApiBaseUrl,
  fetchDashboardSnapshot,
  getStoredApiToken,
  storeApiToken,
} from "@/lib/caam-api";

interface StatCardProps {
  title: string;
  value: string | number;
  change?: string;
  trend?: "up" | "down" | "neutral";
  icon: React.ReactNode;
}

type ActivityTone = "success" | "warning" | "error";

const activityToneByType: Record<string, ActivityTone> = {
  deactivate: "warning",
  error: "error",
};

const activityToneClasses: Record<ActivityTone, string> = {
  error: "text-danger",
  success: "text-success",
  warning: "text-warning",
};

const activityToneIcons = {
  error: AlertCircle,
  success: CheckCircle2,
  warning: AlertCircle,
} satisfies Record<ActivityTone, typeof AlertCircle>;

function StatCard({ title, value, change, trend, icon }: StatCardProps) {
  return (
    <motion.div
      initial={{ opacity: 0, y: 20 }}
      animate={{ opacity: 1, y: 0 }}
    >
      <Card className="p-6">
        <div className="flex items-start justify-between">
          <div>
            <p className="text-sm text-muted">{title}</p>
            <p className="mt-2 text-3xl font-semibold">{value}</p>
            {change && (
              <p
                className={`mt-1 text-sm ${
                  trend === "up"
                    ? "text-success"
                    : trend === "down"
                      ? "text-danger"
                      : "text-muted"
                }`}
              >
                {change}
              </p>
            )}
          </div>
          <div className="rounded-lg bg-accent/10 p-3 text-accent">{icon}</div>
        </div>
      </Card>
    </motion.div>
  );
}

export default function DashboardPage() {
  const router = useRouter();
  const [token, setToken] = useState("");
  const [tokenInput, setTokenInput] = useState("");
  const [snapshot, setSnapshot] = useState<DashboardSnapshot | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [lastLoaded, setLastLoaded] = useState<string | null>(null);
  const [copyStatus, setCopyStatus] = useState<string | null>(null);
  const apiBaseUrl = defaultApiBaseUrl();

  useEffect(() => {
    const initialToken =
      getStoredApiToken() || process.env.NEXT_PUBLIC_CAAM_API_TOKEN || "";
    setToken(initialToken);
    setTokenInput(initialToken);
  }, []);

  const loadData = useCallback(async () => {
    if (token.trim() === "") {
      return;
    }
    setLoading(true);
    setError(null);
    try {
      const nextSnapshot = await fetchDashboardSnapshot(token, apiBaseUrl);
      setSnapshot(nextSnapshot);
      setLastLoaded(new Date().toLocaleTimeString());
    } catch (err) {
      setSnapshot(null);
      setError(err instanceof Error ? err.message : "Unable to load dashboard");
    } finally {
      setLoading(false);
    }
  }, [apiBaseUrl, token]);

  useEffect(() => {
    void loadData();
  }, [loadData]);

  const searchQuery = useMemo(() => {
    if (typeof window === "undefined") {
      return "";
    }
    return (
      new URLSearchParams(window.location.search).get("q")?.trim().toLowerCase() ??
      ""
    );
  }, []);

  const profiles = snapshot?.profiles.profiles ?? [];
  const activity = snapshot?.activity.events ?? [];
  const filteredProfiles = filterProfiles(profiles, searchQuery);
  const filteredActivity = filterActivity(activity, searchQuery);
  const activeProfiles = profiles.filter((profile) => profile.active).length;
  const healthErrors = snapshot?.usage.entries.reduce(
    (total, entry) => total + entry.health_error_count_1h,
    0,
  ) ?? 0;
  const loggedInTools = snapshot?.status.tools.filter((tool) => tool.logged_in).length ?? 0;
  const totalTools = snapshot?.status.tools.length ?? 0;
  const healthyCoordinators =
    snapshot?.coordinators.coordinators.filter(
      (coordinator) => coordinator.status === "healthy",
    ).length ?? 0;
  const totalCoordinators = snapshot?.coordinators.coordinators.length ?? 0;

  function connect(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const nextToken = tokenInput.trim();
    storeApiToken(nextToken);
    setToken(nextToken);
  }

  async function copyTokenCommand() {
    const command = "caam serve --show-token";
    try {
      await navigator.clipboard.writeText(command);
      setCopyStatus("Copied");
    } catch {
      setCopyStatus(command);
    }
  }

  function scrollToProfiles() {
    document.getElementById("profiles")?.scrollIntoView({ block: "start" });
  }

  return (
    <DashboardLayout>
      <div className="space-y-8">
        <div>
          <h1 className="text-2xl font-semibold">Dashboard</h1>
          <p className="mt-1 text-muted">
            Local control-plane state from the CAAM API
          </p>
        </div>

        <Card>
          <CardHeader>
            <CardTitle>Local API Connection</CardTitle>
          </CardHeader>
          <CardContent className="space-y-4">
            <form className="grid gap-3 md:grid-cols-[1fr_auto]" onSubmit={connect}>
              <label className="space-y-1 text-sm">
                <span className="text-muted">Bearer token</span>
                <input
                  aria-label="API token"
                  className="h-10 w-full rounded-lg border border-border bg-background px-3 text-sm focus:border-accent focus:outline-none focus:ring-1 focus:ring-accent"
                  onChange={(event) => setTokenInput(event.target.value)}
                  placeholder="Paste token from caam serve --show-token"
                  type="password"
                  value={tokenInput}
                />
              </label>
              <Button className="self-end" type="submit" variant="primary">
                Connect
              </Button>
            </form>
            <div className="flex flex-wrap items-center gap-3 text-sm text-muted">
              <span>API: {apiBaseUrl}</span>
              {lastLoaded && <span>Last refresh: {lastLoaded}</span>}
              {loading && <span>Refreshing...</span>}
              {error && <span className="text-danger">{error}</span>}
            </div>
          </CardContent>
        </Card>

        <div className="grid gap-6 sm:grid-cols-2 lg:grid-cols-4">
          <StatCard
            title="Active Profiles"
            value={snapshot ? activeProfiles : "--"}
            change={
              snapshot
                ? `${snapshot.profiles.count} tracked profiles`
                : "Connect API to load"
            }
            trend="neutral"
            icon={<Users className="h-6 w-6" />}
          />
          <StatCard
            title="Health Errors (1h)"
            value={snapshot ? healthErrors : "--"}
            change={snapshot ? snapshot.usage.metric : "No health data loaded"}
            trend={healthErrors > 0 ? "down" : "neutral"}
            icon={<Activity className="h-6 w-6" />}
          />
          <StatCard
            title="Logged In Providers"
            value={snapshot ? `${loggedInTools}/${totalTools}` : "--"}
            change={snapshot ? "From live auth files" : "Connect API to load"}
            trend="neutral"
            icon={<Key className="h-6 w-6" />}
          />
          <StatCard
            title="Coordinators"
            value={snapshot ? `${healthyCoordinators}/${totalCoordinators}` : "--"}
            change={
              snapshot && totalCoordinators === 0
                ? "No coordinators configured"
                : "Live coordinator probes"
            }
            trend="neutral"
            icon={<ArrowUpRight className="h-6 w-6" />}
          />
        </div>

        <div className="grid gap-6 lg:grid-cols-2">
          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: 0.1 }}
          >
            <Card id="activity">
              <CardHeader>
                <CardTitle>Recent Activity</CardTitle>
              </CardHeader>
              <div className="divide-y divide-border">
                {filteredActivity.length === 0 && (
                  <EmptyState
                    message={
                      snapshot
                        ? "No activity events matched the current filter."
                        : "Connect the local API to load recorded activity."
                    }
                  />
                )}
                {filteredActivity.map((item) => {
                  const tone = activityTone(item);
                  const ActivityIcon = activityToneIcons[tone];

                  return (
                    <div
                      key={`${item.timestamp}-${item.tool}-${item.profile}-${item.type}`}
                      className="flex items-start gap-3 px-6 py-4"
                    >
                      <ActivityIcon
                        className={`mt-0.5 h-5 w-5 ${activityToneClasses[tone]}`}
                      />
                      <div className="flex-1">
                        <p className="text-sm">{item.message}</p>
                        <p className="mt-1 text-xs text-muted">
                          {formatTimestamp(item.timestamp)}
                        </p>
                      </div>
                    </div>
                  );
                })}
              </div>
            </Card>
          </motion.div>

          <motion.div
            initial={{ opacity: 0, y: 20 }}
            animate={{ opacity: 1, y: 0 }}
            transition={{ delay: 0.2 }}
          >
            <Card>
              <CardHeader>
                <CardTitle>Quick Actions</CardTitle>
              </CardHeader>
              <CardContent className="grid grid-cols-2 gap-4">
                <Button
                  className="h-auto flex-col p-4"
                  disabled={!token || loading}
                  onClick={() => void loadData()}
                  variant="secondary"
                >
                  <RefreshCw className="h-6 w-6 text-accent" />
                  <span>Refresh Data</span>
                </Button>
                <Button
                  className="h-auto flex-col p-4"
                  disabled={!snapshot}
                  onClick={scrollToProfiles}
                  variant="secondary"
                >
                  <Users className="h-6 w-6 text-accent" />
                  <span>View Profiles</span>
                </Button>
                <Button
                  className="h-auto flex-col p-4"
                  onClick={() => void copyTokenCommand()}
                  variant="secondary"
                >
                  <Copy className="h-6 w-6 text-accent" />
                  <span>{copyStatus ?? "Copy Token Command"}</span>
                </Button>
                <Button
                  className="h-auto flex-col p-4"
                  onClick={() => router.push("/debug")}
                  variant="secondary"
                >
                  <Settings className="h-6 w-6 text-accent" />
                  <span>Diagnostics</span>
                </Button>
              </CardContent>
            </Card>
          </motion.div>
        </div>

        <Card id="profiles">
          <CardHeader>
            <CardTitle>Profiles</CardTitle>
          </CardHeader>
          <div className="divide-y divide-border">
            {filteredProfiles.length === 0 && (
              <EmptyState
                message={
                  snapshot
                    ? "No profiles matched the current filter."
                    : "Connect the local API to load profiles."
                }
              />
            )}
            {filteredProfiles.map((profile) => (
              <ProfileRow key={`${profile.tool}/${profile.name}`} profile={profile} />
            ))}
          </div>
        </Card>
      </div>
    </DashboardLayout>
  );
}

function EmptyState({ message }: { message: string }) {
  return <p className="px-6 py-5 text-sm text-muted">{message}</p>;
}

function ProfileRow({ profile }: { profile: ProfileInfo }) {
  return (
    <div className="grid gap-3 px-6 py-4 text-sm sm:grid-cols-[8rem_1fr_auto]">
      <span className="font-medium">{profile.tool}</span>
      <span>{profile.name}</span>
      <div className="flex flex-wrap gap-2">
        {profile.active && <Badge tone="accent">active</Badge>}
        {profile.system && <Badge tone="neutral">system</Badge>}
        {profile.health && (
          <Badge tone={profile.health.status === "healthy" ? "success" : "warning"}>
            {profile.health.status}
          </Badge>
        )}
      </div>
    </div>
  );
}

function filterProfiles(profiles: ProfileInfo[], query: string) {
  if (query === "") {
    return profiles;
  }
  return profiles.filter((profile) =>
    `${profile.tool} ${profile.name} ${profile.health?.status ?? ""}`
      .toLowerCase()
      .includes(query),
  );
}

function filterActivity(events: ActivityEntry[], query: string) {
  if (query === "") {
    return events;
  }
  return events.filter((event) =>
    `${event.type} ${event.tool} ${event.profile} ${event.message}`
      .toLowerCase()
      .includes(query),
  );
}

function activityTone(event: ActivityEntry): ActivityTone {
  return activityToneByType[event.type] ?? "success";
}

function formatTimestamp(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return date.toLocaleString();
}
