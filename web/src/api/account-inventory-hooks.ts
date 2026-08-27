import { useMutation } from "@tanstack/react-query";
import type { AccountInventoryApi, AccountInventoryFilters } from "./account-inventory-types";

export function useAccountInventoryQuery(api: AccountInventoryApi, csrfToken: string) {
  return useMutation({
    mutationKey: ["account-inventory", "query"],
    mutationFn: (filters: AccountInventoryFilters) => api.query(csrfToken, filters),
    gcTime: 0,
    retry: false,
  });
}
