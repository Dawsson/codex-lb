import { Route, ShieldCheck } from "lucide-react";

import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { AccountAliasForm } from "@/features/accounts/components/account-alias-form";
import { AccountProxyBinding } from "@/features/accounts/components/account-proxy-binding";
import type { AccountRoutingPolicy, AccountSummary } from "@/features/accounts/schemas";
import type { AccountProxyBindingRequest, UpstreamProxyAdmin } from "@/features/settings/schemas";

export type AccountSettingsDialogProps = {
  account: AccountSummary;
  busy: boolean;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSetAlias: (accountId: string, alias: string | null) => Promise<unknown>;
  onRoutingPolicyChange: (accountId: string, routingPolicy: AccountRoutingPolicy) => void;
  onSecurityWorkAuthorizedChange: (accountId: string, enabled: boolean) => void;
  upstreamProxyAdmin?: UpstreamProxyAdmin | null;
  onProxyBindingSave?: (accountId: string, payload: AccountProxyBindingRequest) => Promise<unknown>;
};

export function AccountSettingsDialog({
  account,
  busy,
  open,
  onOpenChange,
  onSetAlias,
  onRoutingPolicyChange,
  onSecurityWorkAuthorizedChange,
  upstreamProxyAdmin = null,
  onProxyBindingSave,
}: AccountSettingsDialogProps) {
  const showRoutingPolicy =
    account.status !== "reauth_required" && account.status !== "deactivated";

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-md">
        <DialogHeader>
          <DialogTitle>Account settings</DialogTitle>
          <DialogDescription>
            Local labels and advanced routing controls for this account.
          </DialogDescription>
        </DialogHeader>

        <div className="space-y-4">
          <AccountAliasForm account={account} busy={busy} onSetAlias={onSetAlias} />

          {showRoutingPolicy ? (
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-muted/20 p-3">
              <div className="flex min-w-36 items-center gap-2 text-sm font-medium">
                <Route className="h-4 w-4 text-muted-foreground" aria-hidden />
                Routing policy
              </div>
              <Select
                value={account.routingPolicy ?? "normal"}
                onValueChange={(value) =>
                  onRoutingPolicyChange(account.accountId, value as AccountRoutingPolicy)
                }
                disabled={busy}
              >
                <SelectTrigger
                  aria-label="Routing policy"
                  size="sm"
                  className="h-8 w-44 text-xs"
                >
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="burn_first">Burn first</SelectItem>
                  <SelectItem value="normal">Normal</SelectItem>
                  <SelectItem value="preserve">Preserve</SelectItem>
                </SelectContent>
              </Select>
            </div>
          ) : null}

          <label className="flex items-center justify-between gap-3 rounded-lg border bg-muted/20 px-3 py-2.5">
            <span className="flex min-w-0 items-center gap-2 text-sm font-medium">
              <ShieldCheck className="h-4 w-4 shrink-0 text-muted-foreground" aria-hidden />
              <span className="min-w-0">
                <span className="block">Trusted Access for Cyber</span>
                <span className="block font-normal text-muted-foreground text-xs">
                  Mark accounts enrolled in OpenAI&apos;s security-work program.
                </span>
              </span>
            </span>
            <Switch
              aria-label="Trusted Access for Cyber"
              checked={account.securityWorkAuthorized ?? false}
              disabled={busy}
              onCheckedChange={(checked) =>
                onSecurityWorkAuthorizedChange(account.accountId, checked)
              }
            />
          </label>

          {onProxyBindingSave ? (
            <AccountProxyBinding
              account={account}
              admin={upstreamProxyAdmin}
              busy={busy}
              onSave={onProxyBindingSave}
            />
          ) : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
