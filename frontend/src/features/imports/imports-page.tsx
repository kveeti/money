import { Field as FormField, Form, setInput, useForm } from "@formisch/react";
import { useEffect, useRef } from "react";
import * as v from "valibot";
import { Link } from "wouter";

import { useBucketsQuery } from "../../api/buckets";
import {
  useCreateImportMutation,
  useInfiniteImportsQuery,
} from "../../api/imports";
import { useMeQuery } from "../../api/user";
import { Button } from "../../ui/button/button";
import { Field } from "../../ui/input/field";
import { FileInput } from "../../ui/input/file-input";
import { BucketPicker } from "../buckets/bucket-picker";
import { useI18n } from "../i18n/use-i18n";
import { useImportDrop } from "./import-drop-context";

const schema = v.object({
  file: v.instance(File, "Choose a file"),
  bucket: v.pipe(v.string(), v.nonEmpty("Select an account")),
});

function sampleCSV() {
  const date = (daysAgo: number) =>
    new Date(Date.now() - daysAgo * 86_400_000).toISOString().slice(0, 10);
  return [
    "date,occurred_at,amount,currency,counterparty,note",
    `${date(1)},,-24.50,EUR,Bookshop,Demo import`,
    `${date(3)},,-8.90,EUR,Lunch Cafe,Demo import`,
    `${date(5)},,-42.15,EUR,Corner Market,Demo import`,
    `${date(8)},,120.00,EUR,Expense refund,Demo import`,
    `${date(11)},,-16.00,EUR,City Transit,Demo import`,
    "",
  ].join("\n");
}

function downloadSampleCSV() {
  const link = document.createElement("a");
  link.href = `data:text/csv;charset=utf-8,${encodeURIComponent(sampleCSV())}`;
  link.download = "dash-demo.csv";
  link.click();
}

export default function ImportsPage() {
  return (
    <div className="mx-auto flex w-full max-w-(--page-width) flex-col gap-14 px-3 pt-6 sm:px-6">
      <div>
        <h1 className="text-lg font-medium mb-3">Import transactions</h1>
        <ImportForm />
      </div>

      <section>
        <h1 className="text-lg font-medium mb-3">Past imports</h1>
        <PastImports />
      </section>
    </div>
  );
}

function ImportForm() {
  const buckets = useBucketsQuery();
  const me = useMeQuery();
  const mutation = useCreateImportMutation();
  const { file: droppedFile, clearFile: clearDroppedFile } = useImportDrop();
  const form = useForm({ schema });

  useEffect(() => {
    if (!droppedFile) return;
    setInput(form, { path: ["file"], input: droppedFile });
    clearDroppedFile();
  }, [clearDroppedFile, droppedFile, form]);

  const onSubmit = async (values: v.InferOutput<typeof schema>) => {
    if (mutation.isPending) return;
    try {
      await mutation.mutateAsync({
        file: values.file,
        bucketId: values.bucket,
      });
    } catch {
      // surfaced via mutation.isError below
    }
  };

  return (
    <Form of={form} className="flex w-full flex-col gap-3" onSubmit={onSubmit}>
      <div className="-mx-3 box-border rounded-[2.25rem] border border-border-subtle bg-form p-6 sm:-mx-6">
        <div className="flex w-full flex-col gap-4 min-[30rem]:grid min-[30rem]:grid-cols-[auto_minmax(0,22rem)] min-[30rem]:items-center min-[30rem]:gap-x-8 min-[30rem]:gap-y-3 min-[30rem]:[&>*]:col-span-full">
          <FormField of={form} path={["file"]}>
            {(field) => (
              <Field label="File" error={field.errors?.[0]}>
                <FileInput
                  {...field.props}
                  aria-label="File"
                  acceptedFileTypes={[".csv", "text/csv"]}
                  files={field.input instanceof File ? [field.input] : []}
                />
              </Field>
            )}
          </FormField>

          <FormField of={form} path={["bucket"]}>
            {(field) => (
              <Field label="Account" error={field.errors?.[0]} as="div">
                <div className="w-full">
                  <BucketPicker
                    kinds={["asset", "liability"]}
                    createKinds={["asset"]}
                    placeholder="Select account"
                    value={
                      buckets.data?.find((b) => b.id === field.input) ?? null
                    }
                    onPick={(bucket) => field.onChange(bucket.id)}
                  />
                </div>
              </Field>
            )}
          </FormField>

          {mutation.isError && (
            <p className="text-base text-danger-fg">{mutation.error.message}</p>
          )}

          <div className="mt-4 flex flex-row-reverse gap-4">
            <Button type="submit" className="w-full">
              {form.isSubmitting ? "Importing…" : "Import"}
            </Button>
            <Button type="reset" variant="ghost">
              Reset
            </Button>
          </div>
        </div>
      </div>

      {me.data?.is_demo && (
        <div className="flex flex-wrap items-center gap-2 rounded-3xl border border-demo-border bg-demo-surface p-3">
          <Button
            type="button"
            variant="demo"
            onClick={() =>
              setInput(form, {
                path: ["file"],
                input: new File([sampleCSV()], "dash-demo.csv", {
                  type: "text/csv",
                }),
              })
            }
          >
            Use sample CSV
          </Button>
          <span className="pl-3 text-sm text-demo-fg">or</span>
          <Button
            type="button"
            variant="demoOutline"
            onClick={downloadSampleCSV}
          >
            Download sample CSV
          </Button>
        </div>
      )}
    </Form>
  );
}

const skeletonRows = [
  { name: "13rem", meta: "16rem" },
  { name: "9rem", meta: "20rem" },
  { name: "11rem", meta: "14rem" },
];

function PastImportsSkeleton() {
  return (
    <ul className="m-0 flex list-none flex-col p-0" aria-hidden="true">
      {skeletonRows.map((row, i) => (
        <li
          key={i}
          className="flex flex-col gap-2 border-b border-gray-200 py-3 last:border-b-0"
        >
          <span
            className="h-[1em] animate-pulse rounded-lg bg-gray-150"
            style={{ inlineSize: row.name }}
          />
          <span
            className="h-[.8em] animate-pulse rounded-lg bg-gray-150"
            style={{ inlineSize: row.meta }}
          />
        </li>
      ))}
    </ul>
  );
}

function PastImports() {
  const imports = useInfiniteImportsQuery();
  const buckets = useBucketsQuery();
  const { f } = useI18n();
  const batches = imports.data?.pages.flatMap((page) => page.rows) ?? [];

  const bucketName = (id: string) =>
    buckets.data?.find((b) => b.id === id)?.name ?? id;

  if (imports.isPending || buckets.isPending) return <PastImportsSkeleton />;
  if (imports.isError) return <p>error: {imports.error.message}</p>;
  if (!batches.length) return <p>no imports yet</p>;

  return (
    <div className="-mx-3 max-h-[calc(5*4.75rem)] overflow-y-auto rounded-2xl border border-gray-200 bg-form sm:-mx-6">
      <ul className="m-0 flex list-none flex-col p-0">
        {batches.map((batch) => {
          const processing =
            batch.status === "uploaded" ||
            batch.status === "processing" ||
            batch.status === "queued" ||
            batch.status === "syncing";
          return (
            <li
              key={batch.id}
              className={`relative grid min-h-[4.75rem] grid-cols-[minmax(0,1fr)_auto] content-center gap-x-3 gap-y-1 border-b border-gray-200 px-6 py-3 last:border-b-0 hover:bg-gray-150 ${processing ? "bg-success-surface" : ""}`}
            >
              <Link
                href={`/imports/${batch.id}`}
                className="absolute inset-0 text-transparent no-underline outline-2 outline-transparent outline-offset-[-2px] focus-visible:outline-gray-500"
              >
                {batch.filename}
              </Link>
              <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap underline">
                {batch.filename}
              </span>
              <span className="text-right text-base text-gray-700">
                {bucketName(batch.bucket_id)} ·{" "}
                {f.longDate(new Date(batch.created_at))}
              </span>
              <span
                className={`col-span-full text-base ${processing ? "font-medium text-success-fg" : "text-gray-700"}`}
              >
                {batch.status === "done"
                  ? `${batch.imported} imported · ${batch.duplicates} duplicates`
                  : batch.status === "failed"
                    ? "Failed"
                    : "Processing… refresh for updates"}
              </span>
            </li>
          );
        })}
      </ul>
      <LoadMore imports={imports} />
    </div>
  );
}

function LoadMore(props: {
  imports: ReturnType<typeof useInfiniteImportsQuery>;
}) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = ref.current;
    if (!element || !props.imports.hasNextPage) return;
    const observer = new IntersectionObserver(([entry]) => {
      if (entry.isIntersecting && !props.imports.isFetchingNextPage) {
        void props.imports.fetchNextPage();
      }
    });
    observer.observe(element);
    return () => observer.disconnect();
  }, [props.imports]);

  if (!props.imports.hasNextPage) return null;
  return (
    <div ref={ref} className="p-3 text-center text-base text-gray-700">
      {props.imports.isFetchingNextPage ? "Loading…" : "Load more"}
    </div>
  );
}
