import {
  ArrowLeftIcon,
  ArrowsRightLeftIcon,
  CalendarDaysIcon,
} from "@heroicons/react/24/outline";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Link, useLocation } from "wouter";

import { useBucketsQuery } from "../../api/buckets";
import {
  useCategorizePostingMutation,
  useDeleteTransactionMutation,
  usePatchTransactionMutation,
  useTransactionQuery,
  useUpdateTransactionEntriesMutation,
  useUnmatchTransferMutation,
  type Posting,
  type Transaction,
} from "../../api/transactions";
import { setSearchParam, useSearchParam } from "../../lib/search-param";
import { useDebouncedValue } from "../../lib/use-debounced-value";
import { AlertDialog } from "../../ui/alert-dialog/alert-dialog";
import {
  createAlertDialogHandle,
  type AlertDialogHandle,
} from "../../ui/alert-dialog/alert-dialog-handle";
import { Button } from "../../ui/button/button";
import { Field } from "../../ui/input/field";
import { Input } from "../../ui/input/input";
import { BucketPicker } from "../buckets/bucket-picker";
import { useI18n } from "../i18n/use-i18n";
import { EditEntriesDialog, type EntryAllocation } from "./edit-entries-dialog";
import { TagsField } from "./transaction-details/tag-field";

export default function TransactionDetailPage(props: {
  params: { id: string };
}) {
  const query = useTransactionQuery(props.params.id);
  const i18n = useI18n();

  if (query.isPending || i18n.isLoading) {
    return (
      <p className="mx-auto max-w-(--page-width) px-3 py-6 sm:px-6">
        Loading transaction…
      </p>
    );
  }
  if (query.isError || i18n.isError) {
    return (
      <div className="mx-auto max-w-(--page-width) px-3 py-6 sm:px-6">
        <Link
          href="/transactions"
          className="text-gray-700 hover:text-gray-950"
        >
          Back to transactions
        </Link>
        <p className="mt-6 text-danger-fg">Could not load transaction.</p>
      </div>
    );
  }

  return <TransactionDetail transaction={query.data} />;
}

function TransactionDetail(props: { transaction: Transaction }) {
  const categoryPosting = getCategoryPosting(props.transaction);
  const categoryPostings = getCategoryPostings(props.transaction);
  const editablePostings = props.transaction.postings.filter(
    (posting) => !posting.imported,
  );
  const accountNames = getAccountNames(props.transaction);
  const importedPosting = props.transaction.postings.find(
    (posting) => posting.imported,
  );
  const entriesOpen = useSearchParam("edit-entries") !== null;
  const updateEntries = useUpdateTransactionEntriesMutation();
  const buckets = useBucketsQuery();
  const editVersion = useRef(props.transaction.latest_posting_timestamp);
  if (!entriesOpen) {
    editVersion.current = props.transaction.latest_posting_timestamp;
  }
  const { f } = useI18n();
  const canEditEntries = Boolean(
    !props.transaction.transfer &&
    importedPosting &&
    editablePostings.length >= 1 &&
    props.transaction.postings.filter((posting) => posting.imported).length ===
      1 &&
    editablePostings.every(
      (posting) => posting.currency === importedPosting.currency,
    ),
  );
  const entryAllocations = canEditEntries
    ? editablePostings.map((posting) => ({
        id: posting.id,
        bucketId: posting.bucket.id,
        amount: posting.amount,
      }))
    : [];

  function openEditor() {
    updateEntries.reset();
    editVersion.current = props.transaction.latest_posting_timestamp;
    setSearchParam("edit-entries", "1");
  }

  function closeEditor() {
    updateEntries.reset();
    setSearchParam("edit-entries", undefined);
  }

  function saveEntries(next: EntryAllocation[]) {
    if (updateEntries.isPending || !importedPosting || !canEditEntries) return;
    const postings = next.flatMap((entry) => {
      const bucket = buckets.data?.find(
        (candidate) => candidate.id === entry.bucketId,
      );
      if (!bucket) return [];
      const current = editablePostings.find(
        (posting) => posting.id === entry.id,
      );
      return [
        {
          ...(entry.id ? { id: entry.id } : {}),
          bucket,
          amount: entry.amount,
          currency: importedPosting.currency,
          statsDate: current?.stats_date ?? null,
          memo: current?.memo ?? "",
        },
      ];
    });
    if (postings.length !== next.length) return;
    updateEntries.mutate(
      {
        transactionId: props.transaction.id,
        expectedLatestPostingTimestamp: editVersion.current,
        postings,
      },
      { onSuccess: closeEditor },
    );
  }

  return (
    <div className="mx-auto flex w-full max-w-(--page-width) flex-col gap-8 px-3 py-4 pb-16 sm:px-6 sm:py-6">
      <header>
        <Link
          href="/transactions"
          className="mb-4 inline-flex items-center gap-1 text-sm text-gray-700 hover:text-gray-950"
        >
          <ArrowLeftIcon className="size-4" aria-hidden="true" />
          Transactions
        </Link>
        <h1 className="text-xl font-semibold text-gray-950">
          {props.transaction.counterparty ||
            props.transaction.description ||
            "Transaction"}
        </h1>
        {props.transaction.description &&
          props.transaction.description !== props.transaction.counterparty && (
            <p className="mt-1 text-gray-800">
              {props.transaction.description}
            </p>
          )}
        {importedPosting && (
          <p className="mt-2 text-xl font-medium text-gray-950">
            {f.amount(importedPosting.amount, importedPosting.currency)}
          </p>
        )}
        <p className="mt-1 flex items-center gap-1.5 text-sm text-gray-700">
          <CalendarDaysIcon className="size-4" aria-hidden="true" />
          {props.transaction.occurred_at
            ? f.longDateTime(new Date(props.transaction.occurred_at))
            : f.longDate(new Date(`${props.transaction.occurred_on}T00:00:00`))}
        </p>
      </header>

      {props.transaction.transfer && (
        <TransferCard transaction={props.transaction} />
      )}

      {canEditEntries && editablePostings.length > 1 && (
        <EntryDetails postings={editablePostings} onEdit={openEditor} />
      )}

      {canEditEntries && editablePostings.length === 1 && (
        <div>
          <Button type="button" variant="outline" onClick={openEditor}>
            Edit entries
          </Button>
        </div>
      )}

      <section
        aria-label="Transaction fields"
        className="-mx-3 rounded-2xl border border-border-subtle bg-form p-6 sm:-mx-6"
      >
        <div className="flex w-full flex-col gap-4 min-[30rem]:grid min-[30rem]:grid-cols-[auto_minmax(0,22rem)] min-[30rem]:items-center min-[30rem]:gap-x-8 min-[30rem]:gap-y-3 min-[30rem]:[&>*]:col-span-full">
          {accountNames.length > 0 && (
            <Field label="Account" as="div">
              <p className="text-gray-950">{accountNames.join(" → ")}</p>
            </Field>
          )}
          <MemoField
            transactionId={props.transaction.id}
            memo={props.transaction.memo}
          />
          {categoryPosting &&
            categoryPostings.length === 1 &&
            canEditEntries && (
              <>
                <CategoryField
                  posting={categoryPosting}
                  transactionId={props.transaction.id}
                />
                <TagsField posting={categoryPosting} />
              </>
            )}
        </div>
      </section>

      {canEditEntries && importedPosting && (
        <EditEntriesDialog
          open={entriesOpen}
          source={{
            counterparty: props.transaction.counterparty,
            description: props.transaction.description,
            amount: importedPosting.amount,
            currency: importedPosting.currency,
            date: props.transaction.occurred_on,
            account: importedPosting.bucket.name,
          }}
          entries={entryAllocations}
          error={updateEntries.error?.message}
          onClose={closeEditor}
          onSubmit={saveEntries}
        />
      )}
    </div>
  );
}

function getAccountNames(transaction: Transaction) {
  return [
    ...new Set(
      transaction.postings
        .filter(
          (posting) =>
            posting.bucket.kind === "asset" ||
            posting.bucket.kind === "liability",
        )
        .map((posting) => posting.bucket.name),
    ),
  ];
}

function getCategoryPostings(transaction: Transaction) {
  return transaction.postings.filter(
    (posting) =>
      posting.bucket.kind === "expense" ||
      posting.bucket.kind === "income" ||
      posting.bucket.kind === "person",
  );
}

function EntryDetails(props: { postings: Posting[]; onEdit: () => void }) {
  return (
    <PostingDetails title="Entries" postings={props.postings}>
      <Button type="button" variant="ghost" onClick={props.onEdit}>
        Edit entries
      </Button>
    </PostingDetails>
  );
}

function PostingDetails(props: {
  title: string;
  postings: Posting[];
  children: ReactNode;
}) {
  const { f } = useI18n();
  return (
    <section className="-mx-3 rounded-2xl border border-border-subtle bg-form p-6 sm:-mx-6">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <h2 className="me-auto font-medium">{props.title}</h2>
        {props.children}
      </div>
      <ul className="flex list-none flex-col">
        {props.postings.map((posting) => (
          <li
            key={posting.id}
            className="grid grid-cols-[minmax(0,1fr)_auto] gap-x-3 border-t border-gray-200 py-2 first:border-t-0"
          >
            <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
              <span className="text-gray-600">
                {posting.amount >= 0 ? "To" : "From"}{" "}
              </span>
              {posting.bucket.name}
            </span>
            <span className="whitespace-nowrap tabular-nums">
              {f.amount(Math.abs(posting.amount), posting.currency)}
            </span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function getCategoryPosting(transaction: Transaction) {
  if (transaction.transfer) return null;
  const postings = transaction.postings.filter(
    (posting) =>
      !posting.imported &&
      (posting.bucket.kind === "expense" || posting.bucket.kind === "income"),
  );
  return postings.length === 1 ? postings[0] : null;
}

function MemoField(props: { transactionId: string; memo: string }) {
  const { mutateAsync, error } = usePatchTransactionMutation();
  const [memo, setMemo] = useState(props.memo);
  const debouncedMemo = useDebouncedValue(memo, 200);
  const queuedMemo = useRef(props.memo);
  const savedMemo = useRef(props.memo);
  const saving = useRef(false);

  useEffect(() => {
    queuedMemo.current = debouncedMemo;
    if (saving.current || queuedMemo.current === savedMemo.current) return;

    saving.current = true;
    async function saveQueuedMemo() {
      while (queuedMemo.current !== savedMemo.current) {
        const nextMemo = queuedMemo.current;
        try {
          await mutateAsync({ id: props.transactionId, memo: nextMemo });
          savedMemo.current = nextMemo;
        } catch {
          // Shown on the field.
          if (queuedMemo.current === nextMemo) break;
        }
      }
      saving.current = false;
    }
    void saveQueuedMemo();
  }, [debouncedMemo, mutateAsync, props.transactionId]);

  return (
    <Field label="Memo" error={error?.message}>
      <Input value={memo} onChange={(event) => setMemo(event.target.value)} />
    </Field>
  );
}

function CategoryField(props: { posting: Posting; transactionId: string }) {
  const buckets = useBucketsQuery();
  const mutation = useCategorizePostingMutation();
  const kind = props.posting.bucket.kind;
  const value =
    buckets.data?.find((bucket) => bucket.id === props.posting.bucket.id) ??
    null;

  return (
    <Field label="Category" error={mutation.error?.message} as="div">
      <div className="w-full">
        <BucketPicker
          kinds={[kind]}
          createKinds={[kind]}
          value={value}
          onPick={(bucket) =>
            mutation.mutate({
              transactionId: props.transactionId,
              postingId: props.posting.id,
              bucket,
            })
          }
          placeholder="Filter categories..."
        />
      </div>
    </Field>
  );
}

function TransferCard(props: { transaction: Transaction }) {
  const transfer = props.transaction.transfer!;
  const [, navigate] = useLocation();
  const remove = useDeleteTransactionMutation();
  const unmatch = useUnmatchTransferMutation();
  const dialogRef = useRef<AlertDialogHandle | null>(null);
  dialogRef.current ??= createAlertDialogHandle();

  function removeThisSide() {
    dialogRef.current?.openWithPayload({
      title: "Return this side to the inbox?",
      description: transfer.match_id
        ? "The other side will remain as an unmatched transfer."
        : "This transaction will return to the inbox.",
      confirmLabel: "Remove this side",
      confirmVariant: "destructive",
      onConfirm: () => {
        remove.mutate(props.transaction.id);
        navigate("/transactions");
      },
    });
  }

  function unmatchBoth() {
    if (!transfer.match_id) return;
    dialogRef.current?.openWithPayload({
      title: "Unmatch both sides?",
      description: "Both imported rows will return to the inbox.",
      confirmLabel: "Unmatch both",
      confirmVariant: "destructive",
      onConfirm: () => {
        unmatch.mutate(transfer.match_id!);
        navigate("/transactions");
      },
    });
  }

  return (
    <section className="rounded-2xl border border-border-subtle bg-form p-4 sm:p-6">
      <div className="flex gap-3">
        <ArrowsRightLeftIcon
          className="mt-0.5 size-5 shrink-0 text-gray-700"
          aria-hidden="true"
        />
        <div className="min-w-0 flex-1">
          <h2 className="font-medium">
            {transfer.unmatched ? "Unmatched transfer" : "Matched transfer"}
          </h2>
          {transfer.counterpart_id && (
            <Link
              href={`/transactions/${transfer.counterpart_id}`}
              className="mt-1 inline-block text-sm text-gray-700 underline hover:text-gray-950"
            >
              View counterpart
            </Link>
          )}
        </div>
      </div>
      <div className="mt-4 flex flex-col gap-2 min-[24rem]:flex-row">
        {transfer.match_id && (
          <Button type="button" variant="outline" onClick={unmatchBoth}>
            Unmatch both
          </Button>
        )}
        <Button type="button" variant="destructive" onClick={removeThisSide}>
          Remove this side
        </Button>
      </div>
      <AlertDialog handle={dialogRef.current} />
    </section>
  );
}
