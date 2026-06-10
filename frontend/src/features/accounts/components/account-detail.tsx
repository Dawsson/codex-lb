import { Settings, User } from "lucide-react";
import { useState } from "react";

import { isEmailLabel } from "@/components/blur-email";
import { Button } from "@/components/ui/button";
import { usePrivacyStore } from "@/hooks/use-privacy";
import { AccountActions } from "@/features/accounts/components/account-actions";
import { AccountSettingsDialog } from "@/features/accounts/components/account-settings-dialog";
import { AccountTokenInfo } from "@/features/accounts/components/account-token-info";
import { AccountUsagePanel } from "@/features/accounts/components/account-usage-panel";
import type {
  AccountRoutingPolicy,
  AccountSummary,
} from "@/features/accounts/schemas";
import { useAccountTrends } from "@/features/accounts/hooks/use-accounts";
import type { AccountProxyBindingRequest, UpstreamProxyAdmin } from "@/features/settings/schemas";
import { formatCompactAccountId } from "@/utils/account-identifiers";
import { formatSlug } from "@/utils/formatters";

export type AccountDetailProps = {
  account: AccountSummary | null;
  showAccountId?: boolean;
  busy: boolean;
  onPause: (accountId: string) => void;
  onResume: (accountId: string) => void;
  onProbe: (accountId: string) => void;
  onSetAlias: (accountId: string, alias: string | null) => Promise<unknown>;
  onDelete: (accountId: string) => void;
  onReauth: () => void;
  onLimitWarmupChange: (accountId: string, enabled: boolean) => void;
  onRoutingPolicyChange: (
    accountId: string,
    routingPolicy: AccountRoutingPolicy,
  ) => void;
  onSecurityWorkAuthorizedChange: (accountId: string, enabled: boolean) => void;
  upstreamProxyAdmin?: UpstreamProxyAdmin | null;
  onProxyBindingSave?: (accountId: string, payload: AccountProxyBindingRequest) => Promise<unknown>;
};

export function AccountDetail({
  account,
  showAccountId = false,
  busy,
  onPause,
  onResume,
  onProbe,
  onSetAlias,
  onDelete,
  onReauth,
  onLimitWarmupChange,
  onRoutingPolicyChange,
  onSecurityWorkAuthorizedChange,
  upstreamProxyAdmin = null,
  onProxyBindingSave,
}: AccountDetailProps) {
  const [settingsOpen, setSettingsOpen] = useState(false);
  const { data: trends } = useAccountTrends(account?.accountId ?? null);
  const blurred = usePrivacyStore((s) => s.blurred);

  if (!account) {
    return (
      <div className="flex flex-col items-center justify-center rounded-xl border border-dashed p-12">
        <div className="flex h-12 w-12 items-center justify-center rounded-xl bg-muted">
          <User className="h-5 w-5 text-muted-foreground" />
        </div>
        <p className="mt-3 text-sm font-medium text-muted-foreground">
          Select an account
        </p>
        <p className="mt-1 text-xs text-muted-foreground/70">
          Choose an account from the list to view details.
        </p>
      </div>
    );
  }

  const title = account.displayName || account.email;
  const titleIsEmail = isEmailLabel(title, account.email);
  const compactId = formatCompactAccountId(account.accountId);
  const emailSubtitle =
    account.displayName && account.displayName !== account.email
      ? account.email
      : null;
  const idSuffix = showAccountId ? ` (${compactId})` : "";
  const workspaceLabel = account.workspaceLabel || account.workspaceId || "Personal / unknown workspace";
  const seatLabel = account.seatType ? ` | ${formatSlug(account.seatType)}` : "";

  return (
    <div
      key={account.accountId}
      className="space-y-4 rounded-xl border bg-card p-5"
    >
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
        <h2 className="text-base font-semibold">
          {titleIsEmail ? (
            <>
              <span className={blurred ? "privacy-blur" : ""}>{title}</span>
              {idSuffix}
            </>
          ) : (
            <>
              {title}
              {!emailSubtitle ? idSuffix : ""}
            </>
          )}
        </h2>
        {emailSubtitle ? (
          <p
            className="mt-0.5 text-xs text-muted-foreground"
            title={
              showAccountId ? `Account ID ${account.accountId}` : undefined
            }
          >
            <span className={blurred ? "privacy-blur" : ""}>
              {emailSubtitle}
            </span>
            {showAccountId ? ` | ID ${compactId}` : ""}
          </p>
        ) : null}
        <p className="mt-0.5 text-xs text-muted-foreground">
          {workspaceLabel} | {formatSlug(account.planType)}{seatLabel}
        </p>
        </div>
        <Button
          type="button"
          size="icon-sm"
          variant="ghost"
          aria-label="Account settings"
          className="shrink-0"
          onClick={() => setSettingsOpen(true)}
        >
          <Settings aria-hidden />
        </Button>
      </div>

      <AccountSettingsDialog
        account={account}
        busy={busy}
        open={settingsOpen}
        onOpenChange={setSettingsOpen}
        onSetAlias={onSetAlias}
        onRoutingPolicyChange={onRoutingPolicyChange}
        onSecurityWorkAuthorizedChange={onSecurityWorkAuthorizedChange}
        upstreamProxyAdmin={upstreamProxyAdmin}
        {...(onProxyBindingSave ? { onProxyBindingSave } : {})}
      />
      <AccountUsagePanel account={account} trends={trends} />
      <AccountTokenInfo account={account} />
      <AccountActions
        account={account}
        busy={busy}
        onPause={onPause}
        onResume={onResume}
        onProbe={onProbe}
        onDelete={onDelete}
        onReauth={onReauth}
        onLimitWarmupChange={onLimitWarmupChange}
      />
    </div>
  );
}
