import { createContext } from "../../lib/create-context";

export const [useEntriesInbox, EntriesInboxContext] = createContext<{
  open: (rowId: string) => void;
}>();
