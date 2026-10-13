import {
  ArrowsRightLeftIcon,
  DocumentPlusIcon,
  MagnifyingGlassIcon,
} from "@heroicons/react/24/outline";
import { isSameDay, isSameYear } from "date-fns";
import { Fragment } from "react";
import { Link } from "wouter";

import {
  useInfiniteTransactionsQuery,
  type Transaction,
} from "../../api/transactions";
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
import { useDebouncedAmountFilters } from "../../lib/use-debounced-amount-filters";
import { Button } from "../../ui/button/button";
import { Checkbox } from "../../ui/checkbox/checkbox";
import { useI18n } from "../i18n/use-i18n";
import { TransactionActionsCombobox } from "./transaction-actions-combobox";
import {
  TransactionFiltersButton,
  useTransactionFilters,
} from "./transaction-filters";
import { toTransactionRow, type TransactionRow } from "./transaction-row";

const asKind =
  <K extends TransactionRow["kind"]>(kind: K) =>
  (row: TransactionRow) =>
    row.kind === kind
      ? (row as Extract<TransactionRow, { kind: K }>)
      : undefined;

export default function TransactionsPage() {
  const searchQuery = useSearchParam("q");
  const dateRange = useSearchParam("range");
  const filters = useTransactionFilters();
  const queryFilters = useDebouncedAmountFilters(filters);

  const setParam = (key: string, value: string | undefined) => {
    setSearchParam(key, value || undefined, { replace: true });
  };

  const selection = useSelection();
  const transactions = useInfiniteTransactionsQuery({
    searchQuery: searchQuery || undefined,
    filters: queryFilters,
  });
  const txns =
    transactions.data?.pages.flatMap((page) => page.transactions) ?? [];
  const visibleIds = txns.map((txn) => txn.id);
  const allVisibleSelected =
    visibleIds.length > 0 &&
    visibleIds.every((id) => selection.selected.has(id));

  return (
    <div>
      <Filterbar
        selectLabel="Select transactions"
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
        onSearch={(value) => setParam("q", value)}
        end={<TransactionFiltersButton />}
      />

      <List
        selection={selection}
        transactions={transactions}
        txns={txns}
        searchQuery={searchQuery}
        dateRange={dateRange}
        filters={filters}
      />

      <FloatingBar selection={selection} transactions={txns} />
    </div>
  );
}

function List(props: {
  selection: UseSelectionReturn;
  transactions: ReturnType<typeof useInfiniteTransactionsQuery>;
  txns: Transaction[];
  searchQuery: string | null;
  dateRange: string | null;
  filters: ReturnType<typeof useTransactionFilters>;
}) {
  const { f, isLoading: i18nLoading, isError: i18nError } = useI18n();

  if (props.transactions.isLoading || i18nLoading) {
    return <ListSkeleton twoLine />;
  }
  if (props.transactions.isError || i18nError) {
    return (
      <p className="mx-auto w-full max-w-(--page-width) px-3 sm:px-6">
        error loading transactions
      </p>
    );
  }

  if (!props.txns.length) {
    const hasFilters = Boolean(
      props.searchQuery ||
      props.filters.direction ||
      props.filters.amount ||
      props.filters.amountMin ||
      props.filters.amountMax ||
      props.filters.categories?.length ||
      props.filters.tags?.length ||
      props.filters.accounts?.length ||
      (props.dateRange !== null && props.dateRange !== "all-time"),
    );
    return hasFilters ? (
      <ListEmptyState
        icon={MagnifyingGlassIcon}
        heading="No matching transactions"
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
                  category: undefined,
                  tag: undefined,
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
        icon={DocumentPlusIcon}
        heading="No transactions yet"
        body="Import a bank statement to get started."
        primaryAction={
          <Link
            href="/imports"
            className={`${listEmptyActionClassName} bg-success-solid text-white hover:bg-success-solid-hover`}
          >
            Import transactions
          </Link>
        }
      />
    );
  }

  const visibleIds = props.txns.map((txn) => txn.id);
  let prevMonth: Date | null = null;
  let prevDate: Date | null = null;
  const today = new Date();

  return (
    <>
      <ul
        className={`-mt-1 flex list-none flex-col ${props.selection.selectMode ? "select-none" : ""}`}
      >
        {props.txns.map((txn) => {
          const date = new Date(`${txn.occurred_on}T00:00:00`);
          let dateHeading: string | null = null;
          if (!prevDate || !isSameDay(date, prevDate)) {
            dateHeading = isSameYear(date, today)
              ? f.shortDate(date)
              : f.longDate(date);
          }
          prevDate = date;

          let monthHeading: string | null = null;
          if (
            !prevMonth ||
            date.getMonth() !== prevMonth.getMonth() ||
            date.getFullYear() !== prevMonth.getFullYear()
          ) {
            monthHeading = isSameYear(date, today)
              ? f.month(date)
              : f.monthYear(date);
          }
          prevMonth = date;

          return (
            <Fragment key={txn.id}>
              {monthHeading && (
                <li
                  role="presentation"
                  className="max-w-(--page-width) mx-auto w-full mt-3"
                >
                  <h2 className="text-gray-900 font-semibold text-[1.6rem] ms-5.5">
                    {monthHeading}
                  </h2>
                </li>
              )}
              {dateHeading && (
                <li
                  role="presentation"
                  className="sticky -top-1 z-1 text-sm text-gray-700 [&+li>*]:border-t-0 sm:top-[calc(var(--nav-height)+var(--filterbar-height)-var(--spacing))]"
                >
                  <h3 className="mx-auto my-1 max-w-(--page-width) rounded-lg bg-gray-125 px-3 py-1 text-2xs font-medium sm:px-6">
                    {dateHeading}
                  </h3>
                </li>
              )}

              <li className="mx-auto w-full max-w-(--page-width) px-3 [content-visibility:auto] [contain-intrinsic-size:auto_60px] sm:px-6">
                <div
                  className={`relative border-t border-gray-200 ${props.selection.selectMode ? "cursor-pointer [&_.transaction-check-slot]:opacity-100 [&_.transaction-slide]:pl-8 [&_.transaction-slide_:where(button,a)]:pointer-events-none" : ""}`}
                  onClick={(event) => {
                    if (!props.selection.selectMode) return;
                    if (event.shiftKey)
                      props.selection.selectThrough(txn.id, visibleIds);
                    else props.selection.toggle(txn.id);
                  }}
                >
                  <div className="transaction-check-slot pointer-events-none absolute start-0 top-1/2 flex -translate-y-1/2 opacity-0 transition-opacity duration-220 ease-[cubic-bezier(.25,.8,.25,1)] motion-reduce:duration-[1ms]">
                    <Checkbox
                      checked={props.selection.selected.has(txn.id)}
                      tabIndex={-1}
                      style={{ pointerEvents: "none" }}
                      readOnly
                    />
                  </div>
                  <div className="transaction-slide relative grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 py-2 [contain:layout] transition-[padding-inline-start] duration-220 ease-[cubic-bezier(.25,.8,.25,1)] motion-reduce:duration-[1ms]">
                    <Link
                      href={`/transactions/${txn.id}`}
                      className="absolute inset-0 rounded-lg"
                      aria-label={`View ${txn.counterparty || txn.description || "transaction"}`}
                    />
                    <Row
                      txn={txn}
                      onFilterTag={(value) =>
                        setSearchParams(
                          { tag: value ? [value] : undefined },
                          { replace: true },
                        )
                      }
                    />
                  </div>
                </div>
              </li>
            </Fragment>
          );
        })}
      </ul>

      {props.transactions.hasNextPage && (
        <div className="mx-auto mb-[calc(var(--nav-height)+var(--filterbar-height)+var(--spacing))] w-full max-w-(--page-width) px-3 sm:mb-0 sm:px-6">
          <Button
            type="button"
            variant="ghost"
            className="w-full"
            onClick={() => {
              if (!props.transactions.isFetchingNextPage)
                props.transactions.fetchNextPage();
            }}
          >
            {props.transactions.isFetchingNextPage ? "loading…" : "Load older"}
          </Button>
        </div>
      )}
    </>
  );
}

function Row(props: { txn: Transaction; onFilterTag: (tag: string) => void }) {
  const { f } = useI18n();
  const row = toTransactionRow(props.txn);

  const simple = asKind("simple")(row);
  if (simple) {
    const who = props.txn.counterparty || props.txn.description;
    return (
      <>
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-medium text-gray-950">
          {who}
        </span>
        <span
          className={`whitespace-nowrap text-base font-medium ${simple.amount >= 0 ? "text-success-fg" : "text-gray-950"}`}
        >
          {f.amount(simple.amount, simple.currency)}
        </span>
        <div className="col-span-full grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3">
          <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
            {simple.category}
            <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
          </span>
          {simple.account && (
            <span className="whitespace-nowrap text-sm font-normal text-gray-700">
              {simple.account}
            </span>
          )}
        </div>
      </>
    );
  }

  const categories = asKind("categories")(row);
  if (categories) {
    const who = props.txn.counterparty || props.txn.description;
    return (
      <>
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-medium text-gray-950">
          {who}
        </span>
        <span
          className={`whitespace-nowrap text-base font-medium ${categories.amount >= 0 ? "text-success-fg" : "text-gray-950"}`}
        >
          {f.amount(categories.amount, categories.currency)}
        </span>
        <div className="col-span-full grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3">
          <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
            {categories.categories.join(", ")}
            <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
          </span>
          <span className="whitespace-nowrap text-sm font-normal text-gray-700">
            {categories.account}
          </span>
        </div>
      </>
    );
  }

  const entries = asKind("entries")(row);
  if (entries) {
    const who = props.txn.counterparty || props.txn.description;
    return (
      <>
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-medium text-gray-950">
          {who}
        </span>
        <span
          className={`whitespace-nowrap text-base font-medium ${entries.amount >= 0 ? "text-success-fg" : "text-gray-950"}`}
        >
          {f.amount(entries.amount, entries.currency)}
        </span>
        <div className="col-span-full grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3">
          <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
            {entries.groups.map((group, index) => (
              <Fragment key={group.direction}>
                {index > 0 && ", "}
                {group.direction} {group.names.join(", ")}
              </Fragment>
            ))}
            <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
          </span>
          <span className="whitespace-nowrap text-sm font-normal text-gray-700">
            {entries.account}
          </span>
        </div>
      </>
    );
  }

  const transfer = asKind("transfer")(row);
  if (transfer) {
    return (
      <>
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-normal">
          <ArrowsRightLeftIcon
            className="mr-1 inline-block size-[1em] align-[-.12em] text-gray-700"
            aria-hidden="true"
          />
          {transfer.from} → {transfer.to}
        </span>
        <span className="whitespace-nowrap text-base font-normal text-gray-700">
          {f.amount(transfer.amount, transfer.currency)}
        </span>
        <div className="col-span-full grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3">
          <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
            Transfer
            <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
          </span>
          {transfer.account && (
            <span className="whitespace-nowrap text-sm font-normal text-gray-700">
              {transfer.account}
            </span>
          )}
        </div>
      </>
    );
  }

  const exchange = asKind("exchange")(row);
  if (exchange) {
    return (
      <>
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-normal">
          <ArrowsRightLeftIcon
            className="mr-1 inline-block size-[1em] align-[-.12em] text-gray-700"
            aria-hidden="true"
          />
          {exchange.from !== exchange.to
            ? `${exchange.from} → ${exchange.to}`
            : exchange.from}
        </span>
        <span className="whitespace-nowrap text-base font-normal text-gray-700">
          {f.amount(exchange.fromAmount, exchange.fromCurrency)} →{" "}
          {f.amount(exchange.toAmount, exchange.toCurrency)}
        </span>
        <div className="col-span-full grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3">
          <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
            Exchange
            <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
          </span>
          {exchange.account && (
            <span className="whitespace-nowrap text-sm font-normal text-gray-700">
              {exchange.account}
            </span>
          )}
        </div>
      </>
    );
  }

  const generic = asKind("generic")(row)!;
  const who = props.txn.counterparty || props.txn.description || "Transaction";
  return (
    <>
      <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-base font-medium text-gray-950">
        {who}
      </span>
      <span />
      <span className="col-span-full min-w-0 overflow-hidden text-ellipsis whitespace-nowrap text-sm font-normal text-gray-700">
        {generic.legs.map((leg, i) => (
          <Fragment key={i}>
            {i > 0 && ", "}
            {leg.name} {f.amount(leg.amount, leg.currency)}
          </Fragment>
        ))}
        <TagList tags={props.txn.tags} onFilter={props.onFilterTag} />
      </span>
    </>
  );
}

function TagList(props: { tags: string[]; onFilter: (tag: string) => void }) {
  return (
    <>
      {props.tags.map((tag) => (
        <button
          key={tag}
          type="button"
          className="relative z-1 ml-2 cursor-pointer border-0 bg-none p-0 text-inherit text-gray-700"
          onClick={() => props.onFilter(tag)}
        >
          #{tag}
        </button>
      ))}
    </>
  );
}

function FloatingBar(props: {
  selection: UseSelectionReturn;
  transactions: Transaction[];
}) {
  const ids = [...props.selection.selected];
  const selectedTransactions = props.transactions.filter((t) =>
    props.selection.selected.has(t.id),
  );

  return (
    <FloatingBarWrap show={props.selection.selectMode}>
      <SelectionCountButton
        count={props.selection.selected.size}
        onClick={props.selection.clearSelection}
      />
      <div className="min-w-0 flex-1">
        <TransactionActionsCombobox
          ids={ids}
          onFinish={props.selection.exitSelect}
          transactions={selectedTransactions}
        />
      </div>
      <CloseButton onClick={props.selection.exitSelect} />
    </FloatingBarWrap>
  );
}
