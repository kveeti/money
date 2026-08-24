import { useLocation, useParams } from "wouter";

import {
  useDeleteImportMutation,
  useForceImportRowMutation,
  useImportQuery,
  useInfiniteDuplicatesQuery,
} from "../../api/imports";
import { Button } from "../../ui/button/button";
import { useI18n } from "../i18n/use-i18n";

function ReportSkeleton() {
  return (
    <div
      className="mx-auto flex w-full max-w-(--page-width) flex-col gap-6 px-3 pt-6 sm:px-6"
      aria-hidden="true"
    >
      <header className="flex min-w-0 flex-col gap-1">
        <span
          className="block h-[1.65rem] animate-pulse rounded-lg bg-gray-150"
          style={{ display: "block", inlineSize: "12rem" }}
        />
        <span
          className="block h-[1em] animate-pulse rounded-lg bg-gray-150"
          style={{ inlineSize: "16rem" }}
        />
      </header>
      <div className="-mx-3 overflow-hidden rounded-2xl border border-gray-200 bg-gray-50 sm:-mx-6">
        <ul className="m-0 flex list-none flex-col p-0">
          {["70%", "55%", "80%"].map((width, i) => (
            <li
              key={i}
              className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-gray-200 px-6 py-4 last:border-b-0 min-[30rem]:grid-cols-[minmax(0,1fr)_auto_auto]"
            >
              <span
                className="h-[1em] animate-pulse rounded-lg bg-gray-150"
                style={{ inlineSize: width }}
              />
            </li>
          ))}
        </ul>
      </div>
    </div>
  );
}

export default function ImportReportPage() {
  const { f, isLoading: i18nLoading, isError: i18nError } = useI18n();
  const params = useParams();
  const id = params.id!;
  const [, navigate] = useLocation();

  const batch = useImportQuery(id);
  const processing =
    batch.data?.status === "uploaded" ||
    batch.data?.status === "processing" ||
    batch.data?.status === "queued" ||
    batch.data?.status === "syncing";

  const dupes = useInfiniteDuplicatesQuery(id, !processing);
  const dupeRows = dupes.data?.pages.flatMap((p) => p.rows) ?? [];

  const forceMutation = useForceImportRowMutation(id);
  const deleteMutation = useDeleteImportMutation();

  const onUndo = () => {
    if (deleteMutation.isPending) return;
    const n = batch.data?.imported ?? 0;
    if (
      window.confirm(
        `Delete this import batch and its ${n} transaction${n === 1 ? "" : "s"}?`,
      )
    )
      deleteMutation.mutate(id, { onSuccess: () => navigate("/imports") });
  };

  if (batch.isPending || i18nLoading) return <ReportSkeleton />;
  if (batch.isError || i18nError)
    return (
      <div className="mx-auto flex w-full max-w-(--page-width) flex-col gap-6 px-3 pt-6 sm:px-6">
        <p className="text-base text-danger-fg">
          error: {batch.error?.message ?? "loading currencies failed"}
        </p>
      </div>
    );

  const data = batch.data;

  return (
    <div className="mx-auto flex w-full max-w-(--page-width) flex-col gap-6 px-3 pt-6 sm:px-6">
      <header className="flex min-w-0 flex-col gap-1 [&>p]:text-base [&>p]:text-gray-700">
        <h1 className="m-0 wrap-anywhere text-xl font-medium">
          {data.filename}
        </h1>
        <p className={processing ? "font-medium text-success-fg!" : undefined}>
          {processing
            ? "Processing…"
            : data.status === "failed"
              ? `Failed${data.error ? `: ${data.error}` : ""}`
              : `${data.imported} imported · ${data.duplicates} duplicates`}
        </p>
      </header>

      {data.parse_errors && data.parse_errors.length > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-base font-medium text-gray-700">Skipped rows</h2>
          {(data.parse_error_count ?? data.parse_errors.length) >
            data.parse_errors.length && (
            <p className="m-0 text-base text-gray-700">
              Showing the first {data.parse_errors.length} of{" "}
              {data.parse_error_count} errors.
            </p>
          )}
          <div className="-mx-3 overflow-hidden rounded-2xl border border-gray-200 bg-gray-50 sm:-mx-6">
            <ul className="m-0 flex list-none flex-col p-0">
              {data.parse_errors.map((error, i) => (
                <li
                  key={i}
                  className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-gray-200 px-6 py-4 last:border-b-0 min-[30rem]:grid-cols-[minmax(0,1fr)_auto_auto]"
                >
                  <span className="flex min-w-0 flex-col">
                    Line {error.line}: {error.error}
                  </span>
                </li>
              ))}
            </ul>
          </div>
        </section>
      )}

      {data.duplicates > 0 && (
        <section className="flex flex-col gap-2">
          <h2 className="text-base font-medium text-gray-700">Duplicates</h2>
          <div className="-mx-3 overflow-hidden rounded-2xl border border-gray-200 bg-gray-50 sm:-mx-6">
            <ul className="m-0 flex list-none flex-col p-0">
              {dupeRows.map((row) => (
                <li
                  key={row.id}
                  className="grid grid-cols-[minmax(0,1fr)_auto] items-center gap-x-3 gap-y-2 border-b border-gray-200 px-6 py-4 last:border-b-0 min-[30rem]:grid-cols-[minmax(0,1fr)_auto_auto]"
                >
                  <span className="flex min-w-0 flex-col">
                    <span>
                      {row.date} · {row.counterparty}
                    </span>
                    {row.duplicate_target && (
                      <span className="text-base text-gray-700">
                        Matches {row.duplicate_target.date} ·{" "}
                        {f.amount(
                          row.duplicate_target.amount,
                          row.duplicate_target.currency,
                        )}
                      </span>
                    )}
                  </span>
                  <span className="whitespace-nowrap tabular-nums">
                    {f.amount(row.amount, row.currency)}
                  </span>
                  <Button
                    type="button"
                    variant="ghost"
                    className="col-span-full justify-self-end min-[30rem]:col-span-1"
                    onClick={() => {
                      if (forceMutation.isPending) return;
                      forceMutation.mutate(row.id);
                    }}
                  >
                    import anyway
                  </Button>
                </li>
              ))}
            </ul>
          </div>
          {forceMutation.isError && (
            <span className="text-base text-danger-fg">
              {forceMutation.error.message}
            </span>
          )}
          {dupes.hasNextPage && (
            <Button
              type="button"
              variant="outline"
              className="self-center"
              onClick={() => dupes.fetchNextPage()}
            >
              {dupes.isFetchingNextPage ? "Loading…" : "Load more"}
            </Button>
          )}
        </section>
      )}

      <div className="flex flex-col items-end gap-2">
        <Button type="button" variant="destructive" onClick={onUndo}>
          {deleteMutation.isPending ? "Undoing…" : "Undo import"}
        </Button>
        {deleteMutation.isError && (
          <span className="text-base text-danger-fg">
            {deleteMutation.error.message}
          </span>
        )}
      </div>
    </div>
  );
}
