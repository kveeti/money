import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
  type InfiniteData,
} from "@tanstack/react-query";

import { api } from "./api";
import { bucketKeys, type Bucket } from "./buckets";
import { restoreQueries, type QuerySnapshot } from "./query-snapshot";

export interface InboxFilters {
  direction?: "in" | "out";
  amount?: string;
  amountMin?: string;
  amountMax?: string;
  currency?: string;
  accounts?: string[];
  occurredFrom?: string;
  occurredBefore?: string;
}

export const inboxKeys = {
  all: ["inbox"] as const,
  lists: ["inbox", "list"] as const,
  list: (props: { searchQuery: string | null; filters: InboxFilters }) => [
    "inbox",
    "list",
    props,
  ],
  detail: (id: string) => ["inbox", "detail", id] as const,
};

export interface InboxItem {
  id: string;
  date: string;
  occurred_at: string | null;
  amount: number;
  currency: string;
  counterparty: string;
  description: string;
  account: string;
}

export interface InboxMatch extends InboxItem {
  kind: "transfer" | "exchange";
}

export interface InboxPage {
  rows: InboxItem[];
  next_cursor: { date: string; id: string } | null;
}

export type InboxCacheSnapshot = QuerySnapshot<InfiniteData<InboxPage>>;

export function useInboxItemQuery(id: string | null, enabled = true) {
  return useQuery({
    queryKey: inboxKeys.detail(id ?? ""),
    queryFn: () => api<InboxItem>(`/api/v1/inbox/${id}`),
    enabled: enabled && id !== null,
  });
}

export function restoreInboxRows(rowIds: string[]) {
  return api<{ restored: number }>("/api/v1/inbox/restore", {
    method: "POST",
    body: JSON.stringify({ row_ids: rowIds }),
  });
}

export function useInfiniteInboxQuery(props: {
  searchQuery: string | null;
  filters: InboxFilters;
}) {
  return useInfiniteQuery({
    queryKey: inboxKeys.list(props),
    queryFn: async ({ pageParam }: { pageParam: InboxPage["next_cursor"] }) => {
      const params = new URLSearchParams();

      if (pageParam) {
        params.set("before_date", pageParam.date);
        params.set("before_id", pageParam.id);
      }

      if (props.searchQuery) params.set("q", props.searchQuery);

      if (props.filters.direction)
        params.set("direction", props.filters.direction);

      if (props.filters.amount) params.set("amount", props.filters.amount);

      if (props.filters.amountMin)
        params.set("amount_min", props.filters.amountMin);

      if (props.filters.amountMax)
        params.set("amount_max", props.filters.amountMax);

      if (props.filters.currency)
        params.set("currency", props.filters.currency);

      for (const account of props.filters.accounts ?? [])
        params.append("account", account);

      if (props.filters.occurredFrom)
        params.set("occurred_from", props.filters.occurredFrom);

      if (props.filters.occurredBefore)
        params.set("occurred_before", props.filters.occurredBefore);

      return api<InboxPage>(
        `/api/v1/inbox${params.size ? `?${params.toString()}` : ""}`,
      );
    },
    initialPageParam: null,
    getNextPageParam: (last: InboxPage) => last.next_cursor,
    placeholderData: keepPreviousData,
  });
}

export type InboxCategoryTarget =
  | { type: "bucket"; bucketId: string }
  | {
      type: "new-bucket";
      kind: "expense" | "income" | "person";
      name: string;
    };

export function useCategorizeInboxMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (input: { rowIds: string[]; target: InboxCategoryTarget }) =>
      api<{
        categorized: number;
        bucket?: Bucket;
      }>("/api/v1/inbox/categorize", {
        method: "POST",
        body: JSON.stringify({
          row_ids: input.rowIds,
          ...(input.target.type === "bucket"
            ? { bucket_id: input.target.bucketId }
            : {
                bucket: {
                  kind: input.target.kind,
                  name: input.target.name,
                },
              }),
        }),
      }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: inboxKeys.all });
      const previous = queryClient.getQueriesData<InfiniteData<InboxPage>>({
        queryKey: inboxKeys.lists,
      });

      queryClient.setQueriesData<InfiniteData<InboxPage>>(
        { queryKey: inboxKeys.lists },
        (data) =>
          data && {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              rows: page.rows.filter((row) => !input.rowIds.includes(row.id)),
            })),
          },
      );

      return { previous };
    },
    onSuccess: (result) => {
      queryClient.invalidateQueries({ queryKey: ["transactions"] });
      queryClient.invalidateQueries({ queryKey: ["stats"] });
      const bucket = result.bucket;
      if (bucket) {
        queryClient.setQueryData<Bucket[]>(bucketKeys.all, (current = []) => [
          ...current,
          bucket,
        ]);
        queryClient.invalidateQueries({ queryKey: bucketKeys.searches });
      }
    },
    onError: (_error, _input, context) =>
      restoreQueries(queryClient, context?.previous ?? []),
    onSettled: () => queryClient.invalidateQueries({ queryKey: inboxKeys.all }),
  });
}

export function useCreateInboxEntriesMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (input: {
      rowId: string;
      postings: { bucketId: string; amount: number }[];
    }) =>
      api<{ created: true }>(`/api/v1/inbox/${input.rowId}/entries`, {
        method: "POST",
        body: JSON.stringify({
          postings: input.postings.map((posting) => ({
            bucket_id: posting.bucketId,
            amount: posting.amount,
          })),
        }),
      }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: inboxKeys.all });
      const previous = queryClient.getQueriesData<InfiniteData<InboxPage>>({
        queryKey: inboxKeys.lists,
      });
      queryClient.setQueriesData<InfiniteData<InboxPage>>(
        { queryKey: inboxKeys.lists },
        (data) =>
          data && {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              rows: page.rows.filter((row) => row.id !== input.rowId),
            })),
          },
      );
      return { previous };
    },
    onError: (_error, _input, context) =>
      restoreQueries(queryClient, context?.previous ?? []),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["transactions"] });
      queryClient.invalidateQueries({ queryKey: ["stats"] });
    },
    onSettled: () => {
      void queryClient.invalidateQueries({ queryKey: inboxKeys.all });
    },
  });
}

export function useMatchInboxMutation() {
  const queryClient = useQueryClient();

  return useMutation({
    mutationFn: (input: { id: string; matchId: string }) =>
      api<{ matched: true }>(`/api/v1/inbox/${input.id}/match`, {
        method: "POST",
        body: JSON.stringify({ match_id: input.matchId }),
      }),
    onMutate: async (input) => {
      await queryClient.cancelQueries({ queryKey: inboxKeys.all });
      const previous = queryClient.getQueriesData<InfiniteData<InboxPage>>({
        queryKey: inboxKeys.lists,
      });

      queryClient.setQueriesData<InfiniteData<InboxPage>>(
        { queryKey: inboxKeys.lists },
        (data) =>
          data && {
            ...data,
            pages: data.pages.map((page) => ({
              ...page,
              rows: page.rows.filter(
                (row) => row.id !== input.id && row.id !== input.matchId,
              ),
            })),
          },
      );

      return { previous };
    },
    onError: (_error, _input, context) =>
      restoreQueries(queryClient, context?.previous ?? []),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ["transactions"] });
      queryClient.invalidateQueries({ queryKey: ["stats"] });
    },
    onSettled: () => queryClient.invalidateQueries({ queryKey: inboxKeys.all }),
  });
}

export function useInboxMatchesQuery(props: {
  id: string;
  searchQuery: string;
}) {
  return useQuery({
    enabled: Boolean(props.id),
    queryKey: ["inbox-matches", props.id, props.searchQuery],
    queryFn: () => {
      const params = new URLSearchParams();

      if (props.searchQuery) params.set("q", props.searchQuery);

      return api<{ source: InboxItem; matches: InboxMatch[] }>(
        `/api/v1/inbox/${props.id}/matches${params.size ? `?${params.toString()}` : ""}`,
      );
    },
    placeholderData: (previous, previousQuery) =>
      previousQuery?.queryKey[1] === props.id ? previous : undefined,
  });
}
