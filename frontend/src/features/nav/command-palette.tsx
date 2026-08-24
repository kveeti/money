import { Combobox } from "@base-ui/react/combobox";
import { useState } from "react";
import { useLocation } from "wouter";

import {
  Dialog,
  DialogBackdrop,
  DialogPopup,
  DialogTitle,
} from "../../ui/dialog/dialog";
import { PopupSearchInput } from "../../ui/input/search-input";
import { pages } from "./pages";

export function CommandPalette(props: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  enableBanking: boolean;
}) {
  const [, navigate] = useLocation();
  const [input, setInput] = useState("");
  const availablePages = pages.filter(
    (page) => page.href !== "/connections" || props.enableBanking,
  );

  function go(href: string) {
    props.onOpenChange(false);
    navigate(href);
  }

  return (
    <Dialog
      open={props.open}
      onOpenChange={props.onOpenChange}
      onOpenChangeComplete={(nextOpen) => {
        if (!nextOpen) setInput("");
      }}
    >
      <DialogBackdrop className="bg-black/5" />
      <DialogPopup className="top-[20vh] left-1/2 w-[min(28rem,calc(100vw-1rem))] origin-top overflow-hidden text-gray-1000">
        <DialogTitle className="sr-only">Go to page</DialogTitle>

        <Combobox.Root<(typeof pages)[number]>
          items={availablePages}
          value={null}
          inline
          open
          inputValue={input}
          autoHighlight
          itemToStringLabel={(page) => page.label}
          onInputValueChange={(nextValue, details) => {
            if (details.reason !== "item-press") setInput(nextValue);
          }}
          onValueChange={(page) => {
            if (page) go(page.href);
          }}
        >
          <Combobox.Input
            render={<PopupSearchInput />}
            placeholder="Search…"
            aria-label="Search pages"
          />
          <Combobox.Empty>
            <p className="p-4 text-center text-gray-600">No results.</p>
          </Combobox.Empty>
          <Combobox.List className="scroll-py-1 overflow-y-auto overscroll-contain py-1 outline-none [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
            {(page: (typeof pages)[number]) => (
              <Combobox.Item
                key={page.href}
                value={page}
                className="mx-1 flex h-8 cursor-default select-none items-center rounded-lg px-3 capitalize outline-none data-highlighted:bg-popover-item-selected data-selected:bg-popover-item-selected"
              >
                {page.label}
              </Combobox.Item>
            )}
          </Combobox.List>
        </Combobox.Root>
      </DialogPopup>
    </Dialog>
  );
}
