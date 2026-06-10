import {
  Activity,
  Pause,
  Play,
  RefreshCw,
  Trash2,
  Zap,
} from "lucide-react";

import { Button } from "@/components/ui/button";
import type { AccountSummary } from "@/features/accounts/schemas";

export type AccountActionsProps = {
  account: AccountSummary;
  busy: boolean;
  onPause: (accountId: string) => void;
  onResume: (accountId: string) => void;
  onProbe: (accountId: string) => void;
  onDelete: (accountId: string) => void;
  onReauth: () => void;
  onLimitWarmupChange: (accountId: string, enabled: boolean) => void;
};

export function AccountActions({
  account,
  busy,
  onPause,
  onResume,
  onProbe,
  onDelete,
  onReauth,
  onLimitWarmupChange,
}: AccountActionsProps) {
  const showOperatorRecoveryAction =
    account.status === "reauth_required" || account.status === "deactivated";
  const probeDisabled =
    busy || account.status === "paused" || showOperatorRecoveryAction;

  return (
    <div className="border-t pt-4">
      <div className="flex flex-wrap gap-2">
        {account.status === "paused" ? (
          <Button
            type="button"
            size="sm"
            className="h-8 gap-1.5 text-xs"
            onClick={() => onResume(account.accountId)}
            disabled={busy}
          >
            <Play className="h-3.5 w-3.5" />
            Resume
          </Button>
        ) : showOperatorRecoveryAction ? null : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-8 gap-1.5 text-xs"
            onClick={() => onPause(account.accountId)}
            disabled={busy}
          >
            <Pause className="h-3.5 w-3.5" />
            Pause
          </Button>
        )}

        {showOperatorRecoveryAction ? (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-8 gap-1.5 text-xs"
            onClick={onReauth}
            disabled={busy}
          >
            <RefreshCw className="h-3.5 w-3.5" />
            Re-authenticate
          </Button>
        ) : null}

        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-8 gap-1.5 text-xs"
          onClick={() => onProbe(account.accountId)}
          disabled={probeDisabled}
        >
          <Activity className="h-3.5 w-3.5" />
          Force probe
        </Button>

        <Button
          type="button"
          size="sm"
          variant="outline"
          className="h-8 gap-1.5 text-xs"
          onClick={() =>
            onLimitWarmupChange(account.accountId, !account.limitWarmupEnabled)
          }
          disabled={busy}
        >
          <Zap className="h-3.5 w-3.5" />
          {account.limitWarmupEnabled ? "Disable warm-up" : "Enable warm-up"}
        </Button>

        <Button
          type="button"
          size="sm"
          variant="destructive"
          className="h-8 gap-1.5 text-xs"
          onClick={() => onDelete(account.accountId)}
          disabled={busy}
        >
          <Trash2 className="h-3.5 w-3.5" />
          Delete
        </Button>
      </div>
    </div>
  );
}
