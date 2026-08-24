import { Redirect } from "wouter";

import { useMeQuery } from "../../api/user";
import ConnectionsPage from "./connections-page";

export function ConnectionsRoute() {
  const me = useMeQuery();
  if (me.isPending) return null;
  if (!me.data?.enable_banking_available) {
    return <Redirect to="/transactions" />;
  }
  return <ConnectionsPage />;
}
