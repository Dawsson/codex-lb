import { Flame, Shield, ShieldCheck } from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";
import { isEmailLabel } from "@/components/blur-email";
import { usePrivacyStore } from "@/hooks/use-privacy";
import { useAccountQuotaDisplayStore } from "@/hooks/use-account-quota-display";
import { StatusBadge } from "@/components/status-badge";
import { MiniQuotaBar } from "@/components/mini-quota-bar";
import type {
  AccountRoutingPolicy,
  AccountSummary,
} from "@/features/accounts/schemas";
import { normalizeStatus } from "@/utils/account-status";
import { formatCompactAccountId } from "@/utils/account-identifiers";
import {
  formatPercentNullable,
  formatQuotaResetLabel,
  formatSlug,
} from "@/utils/formatters";

export type AccountListItemProps = {
  account: AccountSummary;
  selected: boolean;
  showAccountId?: boolean;
  onSelect: (accountId: string) => void;
};

export function AccountListItem({
  account,
  selected,
  showAccountId = false,
  onSelect,
}: AccountListItemProps) {
  const blurred = usePrivacyStore((s) => s.blurred);
  const quotaDisplay = useAccountQuotaDisplayStore((s) => s.quotaDisplay);
  const status = normalizeStatus(account.status);
  const title = account.alias?.trim() || account.displayName || account.email;
  const titleIsEmail = isEmailLabel(title, account.email);
  const emailSubtitle =
    account.displayName && account.displayName !== account.email ? account.email : null;
  const metaSubtitle = buildAccountMetaSubtitle(account, showAccountId);
  const primary = account.usage?.primaryRemainingPercent ?? null;
  const secondary = account.usage?.secondaryRemainingPercent ?? null;
  const monthly = account.usage?.monthlyRemainingPercent ?? null;
  const hasPrimaryWindow =
    account.windowMinutesPrimary != null ||
    primary !== null ||
    account.resetAtPrimary != null;
  const hasSecondaryWindow =
    account.windowMinutesSecondary != null ||
    secondary !== null ||
    account.resetAtSecondary != null;
  const hasMonthlyWindow =
    account.windowMinutesMonthly != null ||
    monthly !== null ||
    account.resetAtMonthly != null;
  const monthlyOnly = hasMonthlyWindow && !hasPrimaryWindow && !hasSecondaryWindow;
  const showMonthlyRow = monthlyOnly;
  const showPrimaryRow =
    !monthlyOnly && hasPrimaryWindow && (quotaDisplay !== "weekly" || !hasSecondaryWindow);
  const showSecondaryRow =
    !monthlyOnly && hasSecondaryWindow && (quotaDisplay !== "5h" || !hasPrimaryWindow);
  const visibleQuotaRows = Number(showPrimaryRow) + Number(showSecondaryRow) + Number(showMonthlyRow);
  const showRoutingPolicy = status !== "reauth" && status !== "deactivated";
  const routingPolicy = account.routingPolicy as AccountRoutingPolicy | undefined;
  const showRoutingIndicator =
    showRoutingPolicy && routingPolicy != null && routingPolicy !== "normal";
  const showCyberIndicator = account.securityWorkAuthorized === true;

  return (
    <button
      type="button"
      onClick={() => onSelect(account.accountId)}
      className={cn(
        "w-full rounded-lg border px-3 py-2.5 text-left transition-all",
        selected
          ? "border-primary/35 bg-primary/6 shadow-sm ring-1 ring-primary/15"
          : "border-border/50 bg-background hover:border-border hover:bg-muted/35",
      )}
    >
      <div className="flex items-start gap-2">
        <div className="min-w-0 flex-1">
          <div className="flex items-start justify-between gap-2">
            <p className="truncate text-sm font-semibold leading-tight">
              {titleIsEmail && blurred ? (
                <span className="privacy-blur">{title}</span>
              ) : (
                title
              )}
            </p>
            <StatusBadge status={status} />
          </div>
          {emailSubtitle || metaSubtitle ? (
            <p
              className="mt-0.5 truncate text-[11px] text-muted-foreground"
              title={showAccountId ? `Account ID ${account.accountId}` : undefined}
            >
              {emailSubtitle ? (
                <>
                  <span className={blurred ? "privacy-blur" : undefined}>{emailSubtitle}</span>
                  {metaSubtitle ? ` · ${metaSubtitle}` : null}
                </>
              ) : (
                metaSubtitle
              )}
            </p>
          ) : null}
        </div>
      </div>

      {visibleQuotaRows > 0 ? (
        <div
          className={cn(
            "mt-2.5 grid gap-2",
            visibleQuotaRows > 1 ? "grid-cols-2" : "grid-cols-1",
          )}
        >
          {showMonthlyRow ? (
            <MiniQuotaRow
              label="Monthly"
              percent={monthly}
              resetAt={account.resetAtMonthly}
            />
          ) : null}
          {showPrimaryRow ? (
            <MiniQuotaRow
              label="5h"
              percent={primary}
              resetAt={account.resetAtPrimary}
            />
          ) : null}
          {showSecondaryRow ? (
            <MiniQuotaRow
              label="Weekly"
              percent={secondary}
              resetAt={account.resetAtSecondary}
            />
          ) : null}
        </div>
      ) : null}

      {showRoutingIndicator || showCyberIndicator ? (
        <div className="mt-2 flex flex-wrap items-center gap-1">
          {showRoutingIndicator ? (
            <RoutingPolicyBadge policy={routingPolicy} />
          ) : null}
          {showCyberIndicator ? (
            <span
              className="inline-flex h-5 items-center gap-1 rounded-md border border-emerald-500/20 bg-emerald-500/10 px-1.5 text-[10px] font-medium text-emerald-700"
              title="Trusted Access for Cyber"
            >
              <ShieldCheck className="h-3 w-3" aria-hidden />
              Cyber
            </span>
          ) : null}
        </div>
      ) : null}
    </button>
  );
}

function buildAccountMetaSubtitle(account: AccountSummary, showAccountId: boolean): string {
  const parts: string[] = [formatSlug(account.planType)];

  const workspaceLabel = account.workspaceLabel || account.workspaceId;
  if (workspaceLabel) {
    parts.push(workspaceLabel);
  }

  if (account.seatType) {
    parts.push(formatSlug(account.seatType));
  }

  if (showAccountId) {
    parts.push(`ID ${formatCompactAccountId(account.accountId)}`);
  }

  return parts.join(" · ");
}

function RoutingPolicyBadge({
  policy,
}: {
  policy: AccountRoutingPolicy | undefined;
}) {
  if (policy === "burn_first") {
    return (
      <Badge
        variant="outline"
        className="h-5 shrink-0 gap-1 border-amber-300/80 bg-amber-50 px-1.5 text-[10px] text-amber-700"
      >
        <Flame className="h-3 w-3" aria-hidden="true" />
        Burn first
      </Badge>
    );
  }
  if (policy === "preserve") {
    return (
      <Badge
        variant="outline"
        className="h-5 shrink-0 gap-1 border-sky-300/80 bg-sky-50 px-1.5 text-[10px] text-sky-700"
      >
        <Shield className="h-3 w-3" aria-hidden="true" />
        Preserve
      </Badge>
    );
  }
  return null;
}

function MiniQuotaRow({
  label,
  percent,
  resetAt,
}: {
  label: string;
  percent: number | null;
  resetAt: string | null | undefined;
}) {
  const resetLabel = formatMiniQuotaResetLabel(resetAt ?? null);

  return (
    <div className="space-y-1">
      <div className="flex items-center justify-between gap-2 text-[11px] leading-none">
        <span className="font-medium text-foreground/80">{label}</span>
        <span className="shrink-0 tabular-nums font-semibold">
          {formatPercentNullable(percent)}
        </span>
      </div>
      <MiniQuotaBar
        aria-label={`${label} credits remaining`}
        percent={percent}
        testId={`mini-quota-track-${label.toLowerCase()}`}
      />
      <div className="truncate text-[10px] text-muted-foreground/80">{resetLabel}</div>
    </div>
  );
}

function formatMiniQuotaResetLabel(resetAt: string | null): string {
  const label = formatQuotaResetLabel(resetAt);
  return label.startsWith("Reset ") ? label : `Reset ${label}`;
}
