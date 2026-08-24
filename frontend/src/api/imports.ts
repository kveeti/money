import {
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";

import { api } from "./api";
import { transactionKeys } from "./transactions";

export const importKeys = {
  all: ["imports"] as const,
  list: () => [...importKeys.all, "list"] as const,
  batch: (id: string) => [...importKeys.all, id] as const,
};

export type ImportStatus =
  | "uploaded"
  | "processing"
  | "queued"
  | "syncing"
  | "done"
  | "failed";

export interface ParseError {
  line: number;
  error: string;
}

export interface ImportBatchSummary {
  id: string;
  bucket_id: string;
  filename: string;
  created_at: string;
  status: ImportStatus;
  error?: string | null;
  imported: number;
  duplicates: number;
  // Only the single-batch report populates these; the list omits them.
  parse_errors?: ParseError[];
  parse_error_count?: number;
}

// The upload response: the file is stored durably and parsed/promoted in the
// background, so counts and parse errors land later via the batch report (poll
// until done).
export interface ImportResult {
  batch_id: string;
  status: ImportStatus;
}

export interface DuplicateTarget {
  date: string;
  occurred_at: string | null;
  amount: number;
  currency: string;
  counterparty: string;
  note: string;
  transaction_id: string | null;
  batch_id: string;
  created_at: string;
}

export interface DuplicateRow {
  id: string;
  date: string;
  occurred_at: string | null;
  amount: number;
  currency: string;
  counterparty: string;
  note: string;
  duplicate_of: string | null;
  duplicate_target: DuplicateTarget | null;
}

interface DuplicatePage {
  rows: DuplicateRow[];
  next_cursor: string | null;
}

interface ImportsPage {
  rows: ImportBatchSummary[];
  next_cursor: { date: string; id: string } | null;
}

export function useInfiniteImportsQuery() {
  return useInfiniteQuery({
    queryKey: importKeys.list(),
    queryFn: ({ pageParam }: { pageParam: ImportsPage["next_cursor"] }) => {
      const query = pageParam
        ? `?before_date=${encodeURIComponent(pageParam.date)}&before_id=${pageParam.id}`
        : "";
      return api<ImportsPage>(`/api/v1/imports${query}`);
    },
    initialPageParam: null,
    getNextPageParam: (last: ImportsPage) => last.next_cursor,
  });
}

// Counts-only poll payload — no rows, so the report page can poll it cheaply
// while a batch of any size promotes. Duplicate rows come from a separate
// paginated endpoint. Polls every 800ms until the batch is done or failed.
export function useImportQuery(id: string) {
  return useQuery({
    queryKey: importKeys.batch(id),
    queryFn: () => api<ImportBatchSummary>(`/api/v1/imports/${id}`),
    refetchInterval: (query) => {
      const status = query.state.data?.status;
      return status === "done" || status === "failed" ? false : 800;
    },
  });
}

// Keyset-paginated duplicate rows for one batch. Cursor is the last id seen;
// next_cursor is null once the list is exhausted. Gated by `enabled` so the
// report page only fetches rows once the batch has finished promoting.
export function useInfiniteDuplicatesQuery(id: string, enabled: boolean) {
  return useInfiniteQuery({
    queryKey: [...importKeys.batch(id), "duplicates"],
    queryFn: ({ pageParam }: { pageParam: string }) =>
      api<DuplicatePage>(
        `/api/v1/imports/${id}/duplicates?limit=50${pageParam ? `&cursor=${pageParam}` : ""}`,
      ),
    enabled,
    initialPageParam: "",
    getNextPageParam: (last: DuplicatePage) => last.next_cursor,
  });
}

export function useCreateImportMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (input: { file: File; bucketId: string }) => {
      const form = new FormData();
      form.append("file", input.file);
      form.append("bucket_id", input.bucketId);
      return api<ImportResult>("/api/v1/imports", {
        method: "POST",
        body: form,
      });
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: importKeys.all });
      queryClient.invalidateQueries({ queryKey: transactionKeys.all });
    },
  });
}

export function useForceImportRowMutation(batchId: string) {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (id: string) =>
      api<void>(`/api/v1/imports/rows/${id}/import`, { method: "POST" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: importKeys.batch(batchId) });
      queryClient.invalidateQueries({ queryKey: transactionKeys.all });
    },
  });
}

export function useDeleteImportMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (id: string) =>
      api<void>(`/api/v1/imports/${id}`, { method: "DELETE" }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: importKeys.all });
      queryClient.invalidateQueries({ queryKey: transactionKeys.all });
    },
  });
}
