import { Combobox } from "@base-ui/react/combobox";
import { ChevronDownIcon, TrashIcon } from "@heroicons/react/24/outline";

import type { Transaction } from "../../api/transactions";
import { useDebouncedValue } from "../../lib/use-debounced-value";
import { AlertDialog } from "../../ui/alert-dialog/alert-dialog";
import { AnimatedHeight } from "../../ui/animated-height/animated-height";
import { Checkbox } from "../../ui/checkbox/checkbox";
import { InputTrigger } from "../../ui/input/input-trigger";
import { PopupSearchInput } from "../../ui/input/search-input";
import type {
  TransactionActionGroup,
  TransactionActionItem,
} from "./transaction-action-types";
import { useTagComboboxState } from "./transaction-details/tag-field";
import { useCategoryActions } from "./use-category-actions";
import { useRemoveTransactionAction } from "./use-remove-transaction-action";
import { useTagOptionKeyboard } from "./use-tag-option-keyboard";
import { useTransactionActionsSession } from "./use-transaction-actions-session";

export function TransactionActionsCombobox(props: {
  ids: string[];
  transactions: Transaction[];
  onFinish: () => void;
}) {
  const session = useTransactionActionsSession();
  const search = session.input.trim();
  const query = useDebouncedValue(search, 50);
  const categories = useCategoryActions({
    search,
    query,
    ids: props.ids,
    selectedCount: props.ids.length,
    onFinish: props.onFinish,
  });
  const newTags = useTagComboboxState({
    query,
    search,
    selectedItems: props.transactions.flatMap((transaction) =>
      transaction.postings.filter(
        (posting) =>
          !posting.imported &&
          (posting.bucket.kind === "expense" ||
            posting.bucket.kind === "income"),
      ),
    ),
  });
  const remove = useRemoveTransactionAction({
    ids: props.ids,
    onFinish: props.onFinish,
  });
  const groups = [
    categories.group,
    {
      id: "tags",
      name: "Change or add tags...",
      items: newTags.items,
    },
    remove.group,
  ].filter((group): group is TransactionActionGroup => group !== null);
  const keyboard = useTagOptionKeyboard({
    groups,
    onToggleTag: newTags.toggle,
  });

  function select(item: TransactionActionItem) {
    if (item.type === "tag") {
      newTags.toggle(item);
    } else if (item.type === "remove") {
      remove.request();
    } else {
      void categories.select(item);
    }
  }

  const isFetching = categories.isFetching || newTags.isFetching;
  const isPending = categories.isPending || newTags.isPending;
  const isError = categories.isError || Boolean(newTags.error);

  return (
    <>
      <Combobox.Root<TransactionActionItem>
        items={groups}
        open={session.open}
        inputValue={session.input}
        filter={null}
        autoHighlight
        itemToStringLabel={(item) => item.name}
        onInputValueChange={(input, details) => {
          if (details.reason === "item-press") return;
          keyboard.reset();
          session.setInput(input);
        }}
        onValueChange={(item) => {
          if (item) select(item);
        }}
        onItemHighlighted={keyboard.onItemHighlighted}
        onOpenChange={(open) => {
          keyboard.reset();
          session.setOpen(open);
        }}
        onOpenChangeComplete={(open) => {
          if (!open) keyboard.reset();
          session.completeOpenChange(open);
        }}
      >
        <Combobox.Trigger render={<InputTrigger />}>
          <span className="min-w-0 flex-1 overflow-hidden text-ellipsis whitespace-nowrap text-(--input-placeholder)">
            Actions
          </span>
          <Combobox.Icon className="flex text-gray-600">
            <ChevronDownIcon className="size-4" aria-hidden="true" />
          </Combobox.Icon>
        </Combobox.Trigger>

        <Combobox.Portal>
          <Combobox.Positioner
            align="start"
            sideOffset={6}
            className="z-10 outline-none"
          >
            <Combobox.Popup
              className="min-w-56 max-w-(--available-width,100vw) origin-(--transform-origin) overflow-hidden rounded-xl border border-popover-border bg-popover text-base text-gray-900 shadow-float transition-[opacity,scale] duration-150 ease-[cubic-bezier(0.16,1,0.3,1)] data-starting-style:scale-[.97] data-starting-style:opacity-0 data-ending-style:scale-[.97] data-ending-style:opacity-0 data-ending-style:duration-120 data-ending-style:ease-[cubic-bezier(0.4,0,1,1)] motion-reduce:duration-[1ms]"
              aria-label="Modify selected transactions"
              aria-busy={isFetching || undefined}
            >
              <Combobox.Input
                render={<PopupSearchInput />}
                placeholder="Filter actions…"
                aria-label="Filter transaction actions"
                onKeyDown={keyboard.onInputKeyDown}
              />

              <AnimatedHeight>
                <div
                  data-transaction-actions-scroll
                  className="max-h-[max(calc(2rem*2.5),calc(2rem*1.5+round(down,var(--cap)-2rem*1.5,2rem)))] scroll-py-1 overflow-y-auto overscroll-contain pb-1 [--cap:min(calc(var(--available-height,100vh)-calc(2rem+var(--spacing))),22rem)] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden"
                >
                  <Combobox.Status>
                    {isPending ? (
                      <div className="flex min-h-8 items-center px-3 text-gray-600">
                        Loading…
                      </div>
                    ) : isError ? (
                      <div className="flex min-h-8 items-center px-3 text-gray-600">
                        Error loading actions
                      </div>
                    ) : null}
                  </Combobox.Status>

                  <Combobox.List className="outline-none">
                    {(group: TransactionActionGroup) => (
                      <Combobox.Group
                        key={group.id}
                        items={group.items}
                        className={`[&+&]:mt-1 [&+&]:border-t [&+&]:border-popover-border ${group.name ? "" : "pt-1"}`}
                      >
                        {group.name && (
                          <Combobox.GroupLabel className="flex h-8 items-center px-3 text-base text-gray-600">
                            {group.name}
                          </Combobox.GroupLabel>
                        )}
                        <Combobox.Collection>
                          {(item: TransactionActionItem) => (
                            <Combobox.Item
                              key={item.id}
                              value={item}
                              className={`mx-1 flex h-8 cursor-default items-center gap-2 rounded-lg px-3 outline-none select-none data-highlighted:bg-popover-item-selected data-selected:bg-popover-item-selected ${item.type === "remove" ? "text-danger-fg" : "text-gray-900"}`}
                              onClick={(event) => {
                                if (
                                  item.type === "tag" &&
                                  event.target instanceof Element &&
                                  event.target.closest("[data-tag-checkbox]")
                                ) {
                                  event.preventBaseUIHandler();
                                  newTags.toggle(item);
                                }
                              }}
                            >
                              {item.type === "tag" ? (
                                <span
                                  className="-m-2 flex cursor-pointer p-2"
                                  data-tag-checkbox
                                >
                                  <Checkbox
                                    className="pointer-events-none"
                                    checked={item.state === "all"}
                                    indeterminate={item.state === "some"}
                                    tabIndex={-1}
                                    aria-label={`${item.state === "all" ? "Remove" : "Add"} tag #${item.value} ${item.state === "all" ? "from" : "to"} selected transactions`}
                                  />
                                </span>
                              ) : item.type === "remove" ? (
                                <TrashIcon
                                  className="size-4 shrink-0"
                                  aria-hidden="true"
                                />
                              ) : null}
                              <span>{item.name}</span>
                            </Combobox.Item>
                          )}
                        </Combobox.Collection>
                      </Combobox.Group>
                    )}
                  </Combobox.List>
                </div>
              </AnimatedHeight>
            </Combobox.Popup>
          </Combobox.Positioner>
        </Combobox.Portal>
      </Combobox.Root>

      <AlertDialog handle={remove.dialogHandle} />
    </>
  );
}
