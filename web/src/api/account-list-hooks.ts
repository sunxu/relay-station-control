import { useCallback, useEffect, useRef } from "react";
import { useMutation } from "@tanstack/react-query";
import type { AccountListApi, AccountListFilters } from "./account-quality-types";

export function useAccountListQuery(api: AccountListApi, csrfToken: string) {
  const controller = useRef<AbortController | undefined>(undefined);
  const mutation = useMutation({
    mutationKey: ["account-list", "query"],
    mutationFn: (filters: AccountListFilters) => {
      controller.current?.abort();
      controller.current = new AbortController();
      return api.accountList(filters.instanceId, filters, csrfToken, controller.current.signal);
    },
    gcTime: 0,
    retry: false,
  });
  useEffect(() => () => controller.current?.abort(), []);
  const mutate = useCallback((filters: AccountListFilters) => mutation.mutate(filters), [mutation]);
  const reset = useCallback(() => { controller.current?.abort(); mutation.reset(); }, [mutation]);
  return { ...mutation, mutate, reset };
}
