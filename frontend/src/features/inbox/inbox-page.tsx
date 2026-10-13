import {
  InboxArrowDownIcon,
  MagnifyingGlassIcon,
} from "@heroicons/react/24/outline";
import { Fragment } from "react";
import { Link } from "wouter";

import {
  useCategorizeInboxMutation,
  useInfiniteInboxQuery,
  type InboxItem,
} from "../../api/inbox";
import { Filterbar } from "../../lib/list-shell/filterbar";
import {
  CloseButton,
  FloatingBarWrap,
  SelectionCountButton,
} from "../../lib/list-shell/floating-bar";
import {
  ListEmptyState,
  listEmptyActionClassName,
} from "../../lib/list-shell/list-empty-state";
import { ListSkeleton } from "../../lib/list-shell/list-skeleton";
import {
  useSelection,
  type UseSelectionReturn,
} from "../../lib/list-shell/selection";
import {
  setSearchParam,
  setSearchParams,
  useSearchParam,
} from "../../lib/search-param";
import { UndoNotice } from "../../lib/undo/undo";
import { useDebouncedAmountFilters } from "../../lib/use-debounced-amount-filters";
import { Button } from "../../ui/button/button";
import { Checkbox } from "../../ui/checkbox/checkbox";
import { BucketPicker } from "../buckets/bucket-picker";
import { useI18n } from "../i18n/use-i18n";
import { BucketComboRoot, BucketComboTrigger } from "./bucket-combo";
import { useEntriesInbox } from "./entries-inbox-context";
import { EntriesInboxTransactions } from "./entries-inbox-dialog";
import { useInboxFilters } from "./inbox-filter-state";
import { InboxFiltersButton } from "./inbox-filters";
import { InboxUndoProvider } from "./inbox-undo";
import { useInboxUndo } from "./inbox-undo-context";
import {
  MatchTransactions,
  useMatchTransactions,
} from "./match-transactions-dialog";

export default function InboxPage() {
  const searchQuery = useSearchParam("q");
  const dateRange = useSearchParam("range");
  const filters = useInboxFilters();
  const queryFilters = useDebouncedAmountFilters(filters);
  const selection = useSelection();
  const inboxQuery = useInfiniteInboxQuery({
    searchQuery,
    filters: queryFilters,
  });
  const inboxItems = inboxQuery.data?.pages.flatMap((p) => p.rows) ?? [];
  const visibleIds = inboxItems.map((item) => item.id);
  const allVisibleSelected =
    visibleIds.length > 0 &&
    visibleIds.every((id) => selection.selected.has(id));

  return (
    <InboxUndoProvider selection={selection}>
      <div>
        <Filterbar
          selectLabel="Select rows"
          selectMode={selection.selectMode}
          onSelectMode={(on) =>
            on ? selection.setSelectMode(true) : selection.exitSelect()
          }
          allVisibleSelected={allVisibleSelected}
          onToggleAll={() =>
            allVisibleSelected
              ? selection.drop(visibleIds)
              : selection.select(visibleIds)
          }
          search={searchQuery || undefined}
          onSearch={(value) => setSearchParam("q", value, { replace: true })}
          end={<InboxFiltersButton />}
        />

        <EntriesInboxTransactions>
          <MatchTransactions>
            <List
              selection={selection}
              searchQuery={searchQuery}
              dateRange={dateRange}
              filters={filters}
              inboxQuery={inboxQuery}
              inboxItems={inboxItems}
              visibleIds={visibleIds}
            />
          </MatchTransactions>
        </EntriesInboxTransactions>

        <FloatingBar selection={selection} />

        <UndoNotice raised={selection.selectMode} />
      </div>
    </InboxUndoProvider>
  );
}

function List(props: {
  selection: UseSelectionReturn;
  searchQuery: string | null;
  dateRange: string | null;
  filters: ReturnType<typeof useInboxFilters>;
  inboxQuery: ReturnType<typeof useInfiniteInboxQuery>;
  inboxItems: InboxItem[];
  visibleIds: string[];
}) {
  const { f, isLoading: i18nLoading, isError: i18nError } = useI18n();
  const matchContext = useMatchTransactions();
  const entriesContext = useEntriesInbox();

  if (props.inboxQuery.isLoading || i18nLoading) {
    return <ListSkeleton />;
  }
  if (props.inboxQuery.isError || i18nError) {
    return (
      <p className="mx-auto w-full max-w-(--page-width) px-3 sm:px-6">
        error loading inbox
      </p>
    );
  }

  let prevDate: string | null = null;
  const hasFilters = Boolean(
    props.searchQuery ||
    props.filters.direction ||
    props.filters.amount ||
    props.filters.amountMin ||
    props.filters.amountMax ||
    props.filters.accounts?.length ||
    (props.dateRange !== null && props.dateRange !== "all-time"),
  );

  return (
    <BucketComboRoot
      onMatchAction={matchContext.openMatchingTo}
      onEntriesAction={entriesContext.open}
    >
      {props.inboxItems.length ? (
        <ul
          className={`-mt-1 flex list-none flex-col ${props.selection.selectMode ? "select-none" : ""}`}
        >
          {props.inboxItems.map((item) => {
            const dateFormatted = f.dateOnly(item.date);
            const showDateHeader = dateFormatted !== prevDate;
            prevDate = dateFormatted;

            return (
              <Fragment key={item.id}>
                {showDateHeader && (
                  <li
                    role="presentation"
                    className="sticky -top-1 z-1 text-base text-gray-700 [&+li>*]:border-t-0 sm:top-[calc(var(--nav-height)+var(--filterbar-height)-var(--spacing))]"
                  >
                    <h2 className="mx-auto my-1 max-w-(--page-width) bg-gray-125 px-3 py-1 text-2xs font-medium sm:rounded-lg sm:px-6">
                      {dateFormatted}
                    </h2>
                  </li>
                )}

                <Item
                  item={item}
                  selection={props.selection}
                  visibleIds={props.visibleIds}
                />
              </Fragment>
            );
          })}
        </ul>
      ) : hasFilters ? (
        <ListEmptyState
          icon={MagnifyingGlassIcon}
          heading="No matching inbox items"
          body="Try adjusting your search or filters to find what you’re looking for."
          secondaryAction={
            <button
              type="button"
              className={`${listEmptyActionClassName} text-gray-900 hover:bg-gray-200`}
              onClick={() =>
                setSearchParams(
                  {
                    q: undefined,
                    direction: undefined,
                    amount: undefined,
                    amount_min: undefined,
                    amount_max: undefined,
                    currency: undefined,
                    account: undefined,
                    range: undefined,
                    start: undefined,
                    end: undefined,
                  },
                  { replace: true },
                )
              }
            >
              Clear filters
            </button>
          }
        />
      ) : (
        <ListEmptyState
          icon={InboxArrowDownIcon}
          heading="No transactions to categorize"
          body="Imported transactions that need categorizing will appear here."
          primaryAction={
            <Link
              href="/imports"
              className={`${listEmptyActionClassName} bg-success-solid text-white hover:bg-success-solid-hover`}
            >
              Import transactions
            </Link>
          }
        />
      )}
      {props.inboxQuery.hasNextPage && (
        <div className="mx-auto mb-[calc(var(--nav-height)+var(--filterbar-height)+var(--spacing))] w-full max-w-(--page-width) px-3 sm:mb-0 sm:px-6">
          <Button
            type="button"
            variant="ghost"
            className="w-full"
            onClick={() => {
              if (!props.inboxQuery.isFetchingNextPage)
                props.inboxQuery.fetchNextPage();
            }}
          >
            {props.inboxQuery.isFetchingNextPage ? "loading…" : "Load older"}
          </Button>
        </div>
      )}
    </BucketComboRoot>
  );
}

function Item(props: {
  item: InboxItem;
  selection: UseSelectionReturn;
  visibleIds: string[];
}) {
  const itemId = props.item.id;
  const { f } = useI18n();

  return (
    <li className="mx-auto w-full max-w-(--page-width) px-3 [content-visibility:auto] [contain-intrinsic-size:auto_60px] sm:px-6">
      <div
        className={`relative border-t border-gray-200 ${props.selection.selectMode ? "cursor-pointer" : ""}`}
        onClick={(event) => {
          if (!props.selection.selectMode) return;
          if (event.shiftKey)
            props.selection.selectThrough(itemId, props.visibleIds);
          else props.selection.toggle(itemId);
        }}
      >
        <div
          className={`pointer-events-none absolute start-0 top-1/2 flex -translate-y-1/2 transition-opacity duration-220 ease-[cubic-bezier(.25,.8,.25,1)] motion-reduce:duration-[1ms] ${props.selection.selectMode ? "opacity-100" : "opacity-0"}`}
        >
          <Checkbox
            checked={props.selection.selected.has(itemId)}
            tabIndex={-1}
            style={{ pointerEvents: "none" }}
            readOnly
          />
        </div>
        <div
          className={`flex contain-layout transition-[padding-inline-start] duration-220 ease-[cubic-bezier(.25,.8,.25,1)] motion-reduce:duration-[1ms] ${props.selection.selectMode ? "ps-8 [&_:where(button,a)]:pointer-events-none" : ""}`}
        >
          <BucketComboTrigger
            rowId={itemId}
            className="-mx-3 grid min-w-0 flex-1 cursor-pointer grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 rounded-lg bg-transparent px-3 py-2 text-start font-[inherit] text-inherit outline-2 outline-transparent outline-offset-[-2px] hover:bg-gray-150 focus-visible:outline-(--input-ring-active) data-popup-open:bg-gray-150"
          >
            <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-medium text-gray-1000">
              {props.item.counterparty || "—"}
            </span>
            <span
              className={`text-end whitespace-nowrap text-base font-medium ${props.item.amount >= 0 ? "text-success-fg" : "text-gray-1000"}`}
            >
              {f.amount(props.item.amount, props.item.currency)}
            </span>
            <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm text-gray-700">
              {props.item.description}
            </span>
            <span className="text-end whitespace-nowrap text-sm text-gray-700">
              {props.item.account}
            </span>
          </BucketComboTrigger>
        </div>
      </div>
    </li>
  );
}

function FloatingBar(props: { selection: UseSelectionReturn }) {
  const categorize = useCategorizeInboxMutation();
  const undo = useInboxUndo();

  return (
    <FloatingBarWrap show={props.selection.selectMode}>
      <SelectionCountButton
        count={props.selection.selected.size}
        onClick={props.selection.clearSelection}
      />
      <div className="min-w-0 flex-1">
        <BucketPicker
          kinds={["expense", "income", "person"]}
          createKinds={["expense", "income", "person"]}
          value={null}
          placeholder={
            props.selection.selected.size > 1
              ? "Categorize all as..."
              : "Categorize as..."
          }
          inputPlaceholder="Filter categories..."
          groupLabel="Categorize as..."
          onPick={(bucket) => {
            const ids = [...props.selection.selected];
            void undo.run(
              ids,
              `Categorized ${ids.length} transaction${ids.length === 1 ? "" : "s"}`,
              () =>
                categorize.mutateAsync({
                  rowIds: ids,
                  target: { type: "bucket", bucketId: bucket.id },
                }),
            );
            props.selection.exitSelect();
          }}
        />
      </div>
      <CloseButton onClick={props.selection.exitSelect} />
    </FloatingBarWrap>
  );
}
