import { useEffect, useState } from "react";

import { useMeQuery } from "../../api/user";
import { CommandPalette } from "./command-palette";
import { ImmediateNavLink } from "./immediate-nav-link";
import { NavMenu } from "./nav-menu";
import { navPages } from "./pages";

export function Nav() {
  const [open, setOpen] = useState(false);
  const me = useMeQuery();
  const enableBanking = me.data?.enable_banking_available === true;

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === "k" && (event.metaKey || event.ctrlKey)) {
        event.preventDefault();
        setOpen((previous) => !previous);
      }
    };
    document.addEventListener("keydown", onKeyDown);
    return () => document.removeEventListener("keydown", onKeyDown);
  }, []);

  return (
    <nav className="fixed inset-x-0 bottom-0 z-10 h-(--nav-height) bg-gray-125/80 backdrop-blur-md sm:sticky sm:top-0 sm:pl-(--scrollbar-pl)">
      <div className="mx-auto flex h-full w-full min-w-0 max-w-(--page-width) justify-between px-1 min-[18rem]:px-3">
        <ul className="flex list-none">
          {navPages.map((page) => (
            <li key={page.href}>
              <ImmediateNavLink href={page.href}>{page.label}</ImmediateNavLink>
            </li>
          ))}
        </ul>
        <NavMenu enableBanking={enableBanking} />
      </div>

      <CommandPalette
        open={open}
        onOpenChange={setOpen}
        enableBanking={enableBanking}
      />
    </nav>
  );
}
