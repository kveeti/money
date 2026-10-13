import { useRef, type ReactNode } from "react";

import {
  useCreateInboxEntriesMutation,
  useInboxItemQuery,
} from "../../api/inbox";
import { setSearchParam, useSearchParam } from "../../lib/search-param";
import { EditEntriesDialog } from "../transactions/edit-entries-dialog";
import { EntriesInboxContext } from "./entries-inbox-context";
import { useInboxUndo } from "./inbox-undo-context";

const entriesParam = "entries-inbox";

export function EntriesInboxTransactions(props: { children: ReactNode }) {
  const rowId = useSearchParam(entriesParam);

  return (
    <EntriesInboxContext.Provider
      value={{ open: (id) => setSearchParam(entriesParam, id) }}
    >
      {props.children}
      <EntriesInboxDialog
        rowId={rowId}
        onClose={() => setSearchParam(entriesParam, undefined)}
      />
    </EntriesInboxContext.Provider>
  );
}

function useActiveRowId(rowId: string | null) {
  const activeRowId = useRef<string | null>(null);
  if (rowId) activeRowId.current = rowId;
  return activeRowId.current;
}

function EntriesInboxDialog(props: {
  rowId: string | null;
  onClose: () => void;
}) {
  const activeRowId = useActiveRowId(props.rowId);
  const row = useInboxItemQuery(activeRowId, props.rowId !== null);
  const entries = useCreateInboxEntriesMutation();
  const undo = useInboxUndo();

  function close() {
    entries.reset();
    props.onClose();
  }

  const source = row.data
    ? {
        counterparty: row.data.counterparty,
        description: row.data.description,
        amount: row.data.amount,
        currency: row.data.currency,
        date: row.data.date,
        account: row.data.account,
      }
    : undefined;

  return (
    <EditEntriesDialog
      open={props.rowId !== null}
      source={source}
      entries={[]}
      error={row.error?.message ?? entries.error?.message}
      onClose={close}
      onSubmit={async (next) => {
        if (entries.isPending || !row.data) return;
        try {
          await undo.run([row.data.id], "Created transaction entries", () =>
            entries.mutateAsync({ rowId: row.data.id, postings: next }),
          );
          close();
        } catch {
          // The dialog keeps the input and shows the mutation error.
        }
      }}
    />
  );
}
