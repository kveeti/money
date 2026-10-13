import {
  Field as FormField,
  FieldArray,
  Form,
  getInput,
  insert,
  remove,
  setErrors,
  setInput,
  useForm,
} from "@formisch/react";
import { XMarkIcon } from "@heroicons/react/24/outline";
import { useRef, type RefObject } from "react";
import * as v from "valibot";

import { useBucketsQuery, type BucketKind } from "../../api/buckets";
import { useCurrenciesQuery } from "../../api/currencies";
import { Button } from "../../ui/button/button";
import {
  Dialog,
  DialogBackdrop,
  DialogPopup,
  DialogTitle,
} from "../../ui/dialog/dialog";
import { Input } from "../../ui/input/input";
import { Select } from "../../ui/input/select";
import { BucketPicker } from "../buckets/bucket-picker";
import { useI18n } from "../i18n/use-i18n";

export interface EntryAllocation {
  id?: string;
  bucketId: string;
  amount: number;
}

export interface EntrySource {
  counterparty: string;
  description: string;
  amount: number;
  currency: string;
  date: string;
  account: string;
}

type EntryDirection = "to" | "from";

interface EntryInput {
  id?: string;
  bucketId: string;
  amount: string;
  direction: EntryDirection;
}

const bucketKinds: BucketKind[] = [
  "asset",
  "liability",
  "expense",
  "income",
  "person",
];

export function EditEntriesDialog(props: {
  open: boolean;
  source?: EntrySource;
  entries: EntryAllocation[];
  error?: string;
  onClose: () => void;
  onSubmit: (entries: EntryAllocation[]) => void;
}) {
  const popupRef = useRef<HTMLDivElement>(null);
  const currencies = useCurrenciesQuery();
  const i18n = useI18n();
  const source = props.source;
  const exponent = source
    ? currencies.data?.find((currency) => currency.code === source.currency)
        ?.exponent
    : undefined;

  return (
    <Dialog
      open={props.open}
      onOpenChange={(open) => {
        if (!open) props.onClose();
      }}
    >
      <DialogBackdrop className="bg-black/10" />
      <DialogPopup
        ref={popupRef}
        className="start-1/2 top-1/2 max-h-[calc(100dvh-1rem)] w-[min(30rem,calc(100vw-1rem))] -translate-y-1/2 text-gray-900 outline-none"
      >
        <DialogTitle className="sr-only">Edit entries</DialogTitle>
        {!source || exponent === undefined || i18n.isLoading ? (
          <p className="p-4">Loading transaction…</p>
        ) : (
          <EntriesForm
            {...props}
            source={source}
            exponent={exponent}
            portalContainer={popupRef}
          />
        )}
      </DialogPopup>
    </Dialog>
  );
}

function EntriesForm(props: {
  source: EntrySource;
  entries: EntryAllocation[];
  exponent: number;
  error?: string;
  portalContainer: RefObject<HTMLElement | null>;
  onClose: () => void;
  onSubmit: (entries: EntryAllocation[]) => void;
}) {
  const buckets = useBucketsQuery();
  const { f } = useI18n();
  const target = -props.source.amount;
  const defaultDirection: EntryDirection = target < 0 ? "from" : "to";
  const emptyEntry = (): EntryInput => ({
    bucketId: "",
    amount: "",
    direction: defaultDirection,
  });
  const form = useForm({
    schema: entriesSchema,
    initialInput: {
      entries: withTrailingEmpty(
        props.entries.map((entry) => ({
          id: entry.id,
          bucketId: entry.bucketId,
          amount:
            entry.amount !== 0
              ? formatMinor(Math.abs(entry.amount), props.exponent)
              : "",
          direction: entry.amount < 0 ? "from" : "to",
        })),
        defaultDirection,
      ),
    },
  });

  function addTrailingEntry(index: number) {
    const entries = getInput(form, { path: ["entries"] }) ?? [];
    if (index !== entries.length - 1 || isStarted(entries[index])) return;
    insert(form, { path: ["entries"], initialInput: emptyEntry() });
  }

  function removeEntry(index: number) {
    const entries = getInput(form, { path: ["entries"] }) ?? [];
    if (entries.length === 1) {
      setInput(form, { path: ["entries", index], input: emptyEntry() });
      return;
    }
    remove(form, { path: ["entries"], at: index });
  }

  function apply(values: { entries: EntryInput[] }) {
    const entries = values.entries.filter(isStarted).map((entry) => {
      const amount = parseMinor(entry.amount, props.exponent);
      return {
        id: entry.id,
        bucketId: entry.bucketId,
        amount:
          amount !== null && entry.direction === "from" ? -amount : amount,
      };
    });
    if (
      entries.some((entry) => entry.amount === null) ||
      entries.reduce((sum, entry) => sum + (entry.amount ?? 0), 0) !== target
    ) {
      setErrors(form, {
        errors: ["Entries must balance."],
      });
      return;
    }
    props.onSubmit(
      entries.map((entry) => ({
        ...(entry.id ? { id: entry.id } : {}),
        bucketId: entry.bucketId,
        amount: entry.amount!,
      })),
    );
  }

  return (
    <Form
      of={form}
      className="flex min-h-0 flex-col overflow-hidden rounded-[inherit]"
      onSubmit={apply}
    >
      <Source source={props.source} />
      <FixedEntry source={props.source} />
      <FieldArray of={form} path={["entries"]}>
        {(fieldArray) => {
          const current = getInput(form, { path: ["entries"] }) ?? [];
          const allocated = current.reduce(
            (sum, entry) =>
              sum +
              (entry?.direction === "from" ? -1 : 1) *
                (parseMinor(entry?.amount ?? "", props.exponent) ?? 0),
            0,
          );
          const remaining = target - allocated;

          return (
            <>
              <div className="min-h-48 overflow-y-auto p-3">
                <div className="flex flex-col gap-3">
                  {fieldArray.items.map((item, index) => (
                    <div
                      key={item}
                      className="grid grid-cols-[max-content_minmax(0,1fr)_auto] items-start gap-2 min-[30rem]:grid-cols-[max-content_minmax(0,1fr)_6rem_auto]"
                    >
                      <FormField
                        of={form}
                        path={["entries", index, "direction"]}
                      >
                        {(field) => (
                          <Select
                            {...field.props}
                            value={field.input ?? defaultDirection}
                            aria-label={`Entry ${index + 1} direction`}
                          >
                            <option value="to">To</option>
                            <option value="from">From</option>
                          </Select>
                        )}
                      </FormField>
                      <FormField
                        of={form}
                        path={["entries", index, "bucketId"]}
                      >
                        {(field) => (
                          <div className="col-span-2 min-[30rem]:col-span-1">
                            <BucketPicker
                              kinds={bucketKinds}
                              createKinds={bucketKinds}
                              value={
                                buckets.data?.find(
                                  (bucket) => bucket.id === field.input,
                                ) ?? null
                              }
                              placeholder="Account, category, or person"
                              inputPlaceholder="Filter entries..."
                              portalContainer={props.portalContainer}
                              onPick={(bucket) => {
                                if (bucket.id) addTrailingEntry(index);
                                field.onChange(bucket.id);
                              }}
                            />
                          </div>
                        )}
                      </FormField>
                      <FormField of={form} path={["entries", index, "amount"]}>
                        {(field) => (
                          <Input
                            {...field.props}
                            value={field.input ?? ""}
                            className="col-start-2 ms-auto max-w-24 min-[30rem]:col-start-auto"
                            inputMode="decimal"
                            autoComplete="off"
                            placeholder="0.00"
                            aria-label={`Entry ${index + 1} amount`}
                            onChange={(event) => {
                              const value = event.currentTarget.value;
                              if (value) addTrailingEntry(index);
                              field.onChange(value);
                            }}
                          />
                        )}
                      </FormField>
                      <Button
                        type="button"
                        variant="ghost"
                        className="w-9 !px-2"
                        aria-label={`Remove entry ${index + 1}`}
                        onClick={() => removeEntry(index)}
                      >
                        <XMarkIcon className="size-3.5" aria-hidden="true" />
                      </Button>
                    </div>
                  ))}
                  {form.errors && (
                    <p className="text-sm text-danger-fg">{form.errors[0]}</p>
                  )}
                  {props.error && (
                    <p className="text-sm text-danger-fg">{props.error}</p>
                  )}
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-end gap-2 border-t border-popover-border p-3">
                {remaining === 0 ? (
                  <p className="me-auto text-sm font-medium text-success-fg">
                    Balanced
                  </p>
                ) : (
                  <p className="me-auto text-sm text-danger-fg">
                    Difference:{" "}
                    {f.amount(Math.abs(remaining), props.source.currency)}
                  </p>
                )}
                <div className="flex shrink-0 items-center gap-2">
                  <Button type="button" variant="ghost" onClick={props.onClose}>
                    Cancel
                  </Button>
                  <Button type="submit">Save entries</Button>
                </div>
              </div>
            </>
          );
        }}
      </FieldArray>
    </Form>
  );
}

const entriesSchema = v.pipe(
  v.object({
    entries: v.array(
      v.object({
        id: v.optional(v.string()),
        bucketId: v.string(),
        amount: v.string(),
        direction: v.picklist(["to", "from"]),
      }),
    ),
  }),
  v.check(
    ({ entries }) => entries.filter(isStarted).length >= 1,
    "Add at least one entry",
  ),
  v.check(
    ({ entries }) =>
      entries
        .filter(isStarted)
        .every((entry) => Boolean(entry.bucketId && entry.amount)),
    "Choose an account or category and enter each amount",
  ),
);

function Source(props: { source: EntrySource }) {
  const { f } = useI18n();
  const date = new Date(`${props.source.date}T00:00:00`);
  const dateLabel =
    date.getFullYear() === new Date().getFullYear()
      ? f.shortDate(date)
      : f.longDate(date);
  return (
    <section className="grid grid-cols-[minmax(0,1fr)_auto] items-baseline gap-x-3 border-b border-popover-border p-3">
      <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap font-medium">
        {props.source.counterparty || props.source.description || "—"}
      </span>
      <span className="text-end whitespace-nowrap text-base text-gray-600">
        {dateLabel}
      </span>
    </section>
  );
}

function FixedEntry(props: { source: EntrySource }) {
  const { f } = useI18n();
  return (
    <section className="border-b border-popover-border px-3 py-2">
      <div className="flex items-baseline justify-between gap-3">
        <span className="min-w-0 overflow-hidden text-ellipsis whitespace-nowrap">
          <span className="text-gray-600">
            {props.source.amount >= 0 ? "To" : "From"}{" "}
          </span>
          {props.source.account}
        </span>
        <span className="whitespace-nowrap tabular-nums">
          {f.amount(Math.abs(props.source.amount), props.source.currency)}
        </span>
      </div>
    </section>
  );
}

function withTrailingEmpty(
  entries: EntryInput[],
  defaultDirection: EntryDirection,
) {
  return entries.length && !isStarted(entries[entries.length - 1])
    ? entries
    : [...entries, { bucketId: "", amount: "", direction: defaultDirection }];
}

function isStarted(entry?: { bucketId?: string; amount?: string }) {
  return Boolean(entry?.bucketId || entry?.amount);
}

function parseMinor(value: string, exponent: number) {
  const normalized = value.trim().replace(",", ".");
  if (!/^\d+(?:\.\d+)?$/.test(normalized)) return null;
  const [whole, fraction = ""] = normalized.split(".");
  if (fraction.length > exponent) return null;
  const amount =
    Number(whole) * 10 ** exponent +
    Number(fraction.padEnd(exponent, "0") || "0");
  return Number.isSafeInteger(amount) && amount > 0 ? amount : null;
}

function formatMinor(value: number, exponent: number) {
  return (value / 10 ** exponent).toFixed(exponent);
}
