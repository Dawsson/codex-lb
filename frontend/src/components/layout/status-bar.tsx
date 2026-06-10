import { useEffect, useState } from "react";
import { Activity, ArrowUpCircle } from "lucide-react";
import { useQuery } from "@tanstack/react-query";

import { getDashboardOverview } from "@/features/dashboard/api";
import { DEFAULT_OVERVIEW_TIMEFRAME } from "@/features/dashboard/schemas";
import { getRuntimeVersion } from "@/features/runtime/api";
import { formatTimeLong } from "@/utils/formatters";

export function StatusBar() {
  const { data: lastSyncAt = null } = useQuery({
    queryKey: ["dashboard", "overview", DEFAULT_OVERVIEW_TIMEFRAME],
    queryFn: () => getDashboardOverview({ timeframe: DEFAULT_OVERVIEW_TIMEFRAME }),
    refetchInterval: 60_000,
    refetchIntervalInBackground: false,
    select: (data) => data.lastSyncAt,
  });

  const { data: runtimeVersion } = useQuery({
    queryKey: ["runtime", "version"],
    queryFn: getRuntimeVersion,
    retry: false,
    staleTime: 6 * 60 * 60 * 1000,
  });
  const lastSync = formatTimeLong(lastSyncAt);
  const [isLive, setIsLive] = useState(false);
  useEffect(() => {
    function check() {
      setIsLive(lastSyncAt ? Date.now() - new Date(lastSyncAt).getTime() < 60_000 : false);
    }
    check();
    const id = setInterval(check, 10_000);
    return () => clearInterval(id);
  }, [lastSyncAt]);

  const currentVersion = runtimeVersion?.currentVersion ?? __APP_VERSION__;
  const latestVersion = runtimeVersion?.latestVersion ?? null;
  const showUpdateAvailable = runtimeVersion?.updateAvailable === true && latestVersion;
  const updateLabel = latestVersion
    ? `New version available: ${latestVersion}. Open release notes.`
    : "New version available. Open release notes.";

  return (
    <footer className="fixed bottom-0 left-0 right-0 z-50 border-t border-white/[0.08] bg-background/50 px-3 py-1.5 shadow-[0_-1px_12px_rgba(0,0,0,0.06)] backdrop-blur-xl backdrop-saturate-[1.8] supports-[backdrop-filter]:bg-background/40 dark:shadow-[0_-1px_12px_rgba(0,0,0,0.25)] sm:px-4">
      <div className="flex w-full items-center justify-between gap-4 text-[11px] text-muted-foreground">
        <span className="inline-flex min-w-0 items-center gap-1.5">
          {isLive ? (
            <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-emerald-500" title="Live" />
          ) : (
            <Activity className="h-3 w-3 shrink-0" aria-hidden="true" />
          )}
          <span className="truncate tabular-nums">{lastSync.time}</span>
        </span>
        <span className="inline-flex shrink-0 items-center gap-1.5 tabular-nums">
          v{currentVersion}
          {showUpdateAvailable ? (
            <a
              aria-label={updateLabel}
              className="inline-flex h-4 w-4 items-center justify-center rounded-full text-amber-500 transition-colors hover:text-amber-400 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-amber-500/60 focus-visible:ring-offset-2"
              href={runtimeVersion.releaseUrl}
              rel="noreferrer"
              target="_blank"
              title={updateLabel}
            >
              <ArrowUpCircle className="h-3.5 w-3.5" aria-hidden="true" />
            </a>
          ) : null}
        </span>
      </div>
    </footer>
  );
}
