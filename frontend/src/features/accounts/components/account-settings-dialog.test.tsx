import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { AccountSettingsDialog } from "@/features/accounts/components/account-settings-dialog";
import { createAccountSummary } from "@/test/mocks/factories";

describe("AccountSettingsDialog", () => {
  it("renders routing policy controls for active accounts", () => {
    const account = createAccountSummary({ routingPolicy: "normal" });

    render(
      <AccountSettingsDialog
        account={account}
        busy={false}
        open
        onOpenChange={vi.fn()}
        onSetAlias={vi.fn().mockResolvedValue(undefined)}
        onRoutingPolicyChange={vi.fn()}
        onSecurityWorkAuthorizedChange={vi.fn()}
      />,
    );

    expect(screen.getByText("Routing policy")).toBeInTheDocument();
    expect(screen.getByRole("combobox", { name: "Routing policy" })).toHaveTextContent("Normal");
  });

  it("lets operators change account routing policy", async () => {
    const user = userEvent.setup();
    const onRoutingPolicyChange = vi.fn();
    const account = createAccountSummary({ routingPolicy: "normal" });

    render(
      <AccountSettingsDialog
        account={account}
        busy={false}
        open
        onOpenChange={vi.fn()}
        onSetAlias={vi.fn().mockResolvedValue(undefined)}
        onRoutingPolicyChange={onRoutingPolicyChange}
        onSecurityWorkAuthorizedChange={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("combobox", { name: "Routing policy" }));
    await user.click(await screen.findByRole("option", { name: "Preserve" }));

    expect(onRoutingPolicyChange).toHaveBeenCalledWith(account.accountId, "preserve");
  });
});
