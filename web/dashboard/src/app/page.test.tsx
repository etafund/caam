import { beforeEach, describe, it, expect, vi } from "vitest";
import { render, screen, waitFor } from "@testing-library/react";
import DashboardPage from "./page";

// Mock next/navigation
vi.mock("next/navigation", () => ({
  usePathname: () => "/",
  useRouter: () => ({
    push: vi.fn(),
    replace: vi.fn(),
    prefetch: vi.fn(),
  }),
}));

// Mock framer-motion to avoid animation issues in tests
vi.mock("framer-motion", () => ({
  motion: {
    div: ({
      children,
      ...props
    }: React.PropsWithChildren<
      React.HTMLAttributes<HTMLDivElement> & {
        layoutId?: string;
        transition?: unknown;
      }
    >) => {
      const divProps = { ...props };
      delete divProps.layoutId;
      delete divProps.transition;
      return <div {...divProps}>{children}</div>;
    },
  },
  AnimatePresence: ({ children }: React.PropsWithChildren) => <>{children}</>,
}));

describe("DashboardPage", () => {
  beforeEach(() => {
    window.sessionStorage.clear();
    vi.restoreAllMocks();
  });

  it("renders the dashboard header", () => {
    render(<DashboardPage />);
    expect(screen.getByRole("heading", { name: "Dashboard" })).toBeInTheDocument();
  });

  it("renders stat cards without fake values before API connection", () => {
    render(<DashboardPage />);
    expect(screen.getByText("Active Profiles")).toBeInTheDocument();
    expect(screen.getByText("Health Errors (1h)")).toBeInTheDocument();
    expect(screen.getByText("Logged In Providers")).toBeInTheDocument();
    expect(screen.getByText("Coordinators")).toBeInTheDocument();
    expect(screen.getAllByText("--")).toHaveLength(4);
    expect(screen.queryByText("1,284")).not.toBeInTheDocument();
    expect(screen.queryByText("+2 this week")).not.toBeInTheDocument();
  });

  it("renders recent activity section", () => {
    render(<DashboardPage />);
    expect(screen.getByText("Recent Activity")).toBeInTheDocument();
  });

  it("renders quick actions section", () => {
    render(<DashboardPage />);
    expect(screen.getByText("Quick Actions")).toBeInTheDocument();
    expect(screen.getByText("Refresh Data")).toBeInTheDocument();
    expect(screen.getByText("View Profiles")).toBeInTheDocument();
    expect(screen.getByText("Copy Token Command")).toBeInTheDocument();
    expect(screen.getAllByText("Diagnostics").length).toBeGreaterThan(0);
  });

  it("does not link sidebar navigation to unimplemented pages", () => {
    render(<DashboardPage />);
    const links = screen.getAllByRole("link").map((link) => ({
      label: link.textContent,
      href: link.getAttribute("href"),
    }));
    expect(links).toEqual(
      expect.arrayContaining([
        { label: "Dashboard", href: "/" },
        { label: "Profiles", href: "/#profiles" },
        { label: "Activity", href: "/#activity" },
        { label: "Diagnostics", href: "/debug" },
      ]),
    );
    expect(links.some((link) => link.href === "/credentials")).toBe(false);
    expect(links.some((link) => link.href === "/sync")).toBe(false);
    expect(links.some((link) => link.href === "/settings")).toBe(false);
  });

  it("loads dashboard data from the local API when a token is stored", async () => {
    window.sessionStorage.setItem("caam.dashboard.auth", "token-1");
    const fetchMock = vi
      .spyOn(globalThis, "fetch")
      .mockImplementation((input, init) => {
        expect(init?.headers).toEqual({ Authorization: "Bearer token-1" });
        const path = String(input).replace("http://127.0.0.1:7891", "");
        const responses: Record<string, unknown> = {
          "/api/v1/status": {
            version: "test",
            timestamp: "2026-07-03T12:00:00Z",
            tools: [
              { tool: "codex", logged_in: true, active_profile: "work" },
              { tool: "claude", logged_in: false },
            ],
          },
          "/api/v1/profiles": {
            count: 2,
            profiles: [
              { tool: "codex", name: "work", active: true, system: false },
              { tool: "claude", name: "backup", active: false, system: false },
            ],
          },
          "/api/v1/usage": {
            window: "1h",
            metric: "health_error_count",
            entries: [
              {
                tool: "codex",
                profile: "work",
                health_error_count_1h: 2,
              },
            ],
          },
          "/api/v1/coordinators": {
            coordinators: [{ id: "local", endpoint: "http://127.0.0.1", status: "healthy" }],
          },
          "/api/v1/activity": {
            count: 1,
            events: [
              {
                timestamp: "2026-07-03T12:00:00Z",
                type: "activate",
                tool: "codex",
                profile: "work",
                message: "Activated codex/work",
              },
            ],
          },
        };
        return Promise.resolve(
          new Response(JSON.stringify(responses[path]), { status: 200 }),
        );
      });

    render(<DashboardPage />);

    await waitFor(() => expect(fetchMock).toHaveBeenCalledTimes(5));
    expect(await screen.findByText("2 tracked profiles")).toBeInTheDocument();
    expect(screen.getByText("health_error_count")).toBeInTheDocument();
    expect(screen.getByText("1/2")).toBeInTheDocument();
    expect(screen.getByText("1/1")).toBeInTheDocument();
    expect(screen.getByText("Activated codex/work")).toBeInTheDocument();
    expect(screen.getByText("work")).toBeInTheDocument();
  });
});
