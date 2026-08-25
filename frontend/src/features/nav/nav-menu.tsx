import { Menu } from "@base-ui/react/menu";
import { ArrowPathIcon, Bars3Icon } from "@heroicons/react/24/outline";
import { Link } from "wouter";

import {
  useEnableBankingConnections,
  useEnableBankingSyncStatus,
  useSyncEnableBankingAccounts,
} from "../../api/enablebanking";
import { menuPages } from "./pages";

export function NavMenu(props: { enableBanking: boolean }) {
  const connections = useEnableBankingConnections(props.enableBanking);
  const syncStatus = useEnableBankingSyncStatus(props.enableBanking);
  const sync = useSyncEnableBankingAccounts();
  const accounts = (connections.data ?? []).flatMap((connection) =>
    connection.accounts.flatMap((account) =>
      account.bucket_id
        ? [{ connectionId: connection.id, accountUid: account.uid }]
        : [],
    ),
  );

  return (
    <Menu.Root>
      <Menu.Trigger
        className="inline-flex h-full items-center px-2 text-gray-900 outline-2 outline-transparent outline-offset-[-2px] hover:bg-gray-200/80 focus-visible:outline-(--input-ring-active) min-[18rem]:px-3"
        aria-label="Open menu"
      >
        <Bars3Icon className="size-4" aria-hidden="true" />
      </Menu.Trigger>
      <Menu.Portal>
        <Menu.Positioner
          align="end"
          sideOffset={6}
          className="z-20 outline-none"
        >
          <Menu.Popup className="min-w-44 origin-(--transform-origin) rounded-xl border border-popover-border bg-popover p-1 text-base text-gray-900 shadow-float outline-none transition-[opacity,scale] duration-150 ease-[cubic-bezier(.16,1,.3,1)] data-ending-style:scale-[.97] data-ending-style:opacity-0 data-starting-style:scale-[.97] data-starting-style:opacity-0 motion-reduce:duration-[1ms]">
            {menuPages
              .filter(
                (page) => page.href !== "/connections" || props.enableBanking,
              )
              .map((page) => (
                <Menu.LinkItem
                  key={page.href}
                  render={<Link href={page.href} />}
                  closeOnClick
                  className="flex h-9 cursor-default items-center rounded-lg px-3 capitalize no-underline outline-none data-highlighted:bg-popover-item-selected"
                >
                  {page.label}
                </Menu.LinkItem>
              ))}
            {accounts.length > 0 && (
              <>
                <Menu.Separator className="my-1 h-px bg-popover-border" />
                <Menu.Item
                  closeOnClick={false}
                  className="flex h-9 cursor-default items-center gap-2 rounded-lg px-3 outline-none data-highlighted:bg-popover-item-selected"
                  onClick={() => {
                    if (!sync.isPending && !syncStatus.data?.syncing)
                      sync.mutate(accounts);
                  }}
                >
                  <ArrowPathIcon
                    className={`size-3.5 ${syncStatus.data?.syncing ? "motion-safe:animate-spin" : ""}`}
                    data-sync-icon
                    aria-hidden="true"
                  />
                  Sync all
                </Menu.Item>
              </>
            )}
            <Menu.Separator className="my-1 h-px bg-popover-border" />
            <form action="/api/v1/auth/logout" method="post">
              <Menu.Item
                render={<button type="submit" />}
                className="flex h-9 w-full cursor-default items-center rounded-lg px-3 outline-none data-highlighted:bg-popover-item-selected"
              >
                Sign out
              </Menu.Item>
            </form>
          </Menu.Popup>
        </Menu.Positioner>
      </Menu.Portal>
    </Menu.Root>
  );
}
