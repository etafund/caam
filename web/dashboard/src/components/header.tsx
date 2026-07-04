"use client";

import { Bell, Search, Moon, Sun, User } from "lucide-react";
import Link from "next/link";
import { useRouter } from "next/navigation";
import { useEffect, useState, useSyncExternalStore } from "react";

// External store for dark mode detection
function subscribeToDarkMode(callback: () => void) {
  const mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
  mediaQuery.addEventListener("change", callback);
  return () => mediaQuery.removeEventListener("change", callback);
}

function getDarkModeSnapshot() {
  return window.matchMedia("(prefers-color-scheme: dark)").matches;
}

function getDarkModeServerSnapshot() {
  return false; // Default to light mode on server
}

export function Header() {
  const router = useRouter();
  const isDark = useSyncExternalStore(
    subscribeToDarkMode,
    getDarkModeSnapshot,
    getDarkModeServerSnapshot
  );
  const [themeOverride, setThemeOverride] = useState<"dark" | "light" | null>(
    () => {
      const storedTheme = getStoredTheme();
      return storedTheme === "dark" || storedTheme === "light"
        ? storedTheme
        : null;
    },
  );
  const [query, setQuery] = useState(
    () =>
      new URLSearchParams(
        typeof window === "undefined" ? "" : window.location.search,
      ).get("q") ?? "",
  );
  const darkModeEnabled = themeOverride ? themeOverride === "dark" : isDark;

  useEffect(() => {
    document.documentElement.classList.toggle("dark", darkModeEnabled);
  }, [darkModeEnabled]);

  function toggleTheme() {
    const nextTheme = darkModeEnabled ? "light" : "dark";
    storeTheme(nextTheme);
    setThemeOverride(nextTheme);
  }

  function submitSearch(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const params = new URLSearchParams(window.location.search);
    const trimmed = query.trim();
    if (trimmed === "") {
      params.delete("q");
    } else {
      params.set("q", trimmed);
    }
    const nextQuery = params.toString();
    router.push(nextQuery ? `/?${nextQuery}` : "/");
  }

  return (
    <header className="flex h-16 items-center justify-between border-b border-border bg-surface px-6">
      <div className="flex flex-1 items-center gap-4">
        <form className="relative max-w-md flex-1" onSubmit={submitSearch}>
          <Search className="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted" />
          <input
            aria-label="Filter dashboard data"
            onChange={(event) => setQuery(event.target.value)}
            type="text"
            placeholder="Filter profiles and activity..."
            value={query}
            className="h-10 w-full rounded-lg border border-border bg-background pl-10 pr-4 text-sm placeholder:text-muted focus:border-accent focus:outline-none focus:ring-1 focus:ring-accent"
          />
          <kbd className="absolute right-3 top-1/2 hidden -translate-y-1/2 rounded border border-border-muted bg-surface-muted px-1.5 py-0.5 text-xs text-muted sm:inline">
            Enter
          </kbd>
        </form>
      </div>

      <div className="flex items-center gap-2">
        <button
          aria-label="Toggle theme"
          className="flex h-9 w-9 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-muted hover:text-foreground"
          onClick={toggleTheme}
          type="button"
        >
          {darkModeEnabled ? (
            <Sun className="h-5 w-5" />
          ) : (
            <Moon className="h-5 w-5" />
          )}
        </button>

        <Link
          aria-label="View activity"
          className="relative flex h-9 w-9 items-center justify-center rounded-lg text-muted transition-colors hover:bg-surface-muted hover:text-foreground"
          href="/#activity"
        >
          <Bell className="h-5 w-5" />
        </Link>

        <Link
          className="flex h-9 items-center gap-2 rounded-lg px-3 text-muted transition-colors hover:bg-surface-muted hover:text-foreground"
          href="/debug"
        >
          <User className="h-5 w-5" />
          <span className="text-sm font-medium">Diagnostics</span>
        </Link>
      </div>
    </header>
  );
}

function getStoredTheme() {
  try {
    return window.localStorage?.getItem("caam.dashboard.theme") ?? null;
  } catch {
    return null;
  }
}

function storeTheme(theme: "dark" | "light") {
  try {
    window.localStorage?.setItem("caam.dashboard.theme", theme);
  } catch {
    return;
  }
}
