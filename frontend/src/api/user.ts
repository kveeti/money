import { useQuery } from "@tanstack/react-query";

import { api } from "./api";

export interface Me {
  id: string;
  email: string;
  home_currency: string;
  enable_banking_available: boolean;
}

export function useMeQuery() {
  return useQuery({
    queryKey: ["me"],
    queryFn: () => api<Me>("/api/v1/users/@me"),
  });
}
