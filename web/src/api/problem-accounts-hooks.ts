import { useCallback, useEffect, useRef } from "react";
import { useMutation } from "@tanstack/react-query";
import type { ProblemAccountQueryRequest } from "./problem-accounts-types";
import type { ProblemAccountsApi } from "./problem-accounts-types";

export function useProblemAccountsQuery(api: ProblemAccountsApi, csrfToken: string) {
  const controller = useRef<AbortController | undefined>(undefined);
  type MutationInput = { request: ProblemAccountQueryRequest; controller: AbortController };
  const mutation = useMutation({
    mutationKey: ["problem-accounts", "query"],
    mutationFn: ({ request, controller: requestController }: MutationInput) => {
      if (requestController.signal.aborted) {
        return Promise.reject(new DOMException("The operation was aborted.", "AbortError"));
      }
      return api.query(request, csrfToken, requestController.signal);
    },
    gcTime: 0,
    retry: false,
  });
  const mutationRef = useRef(mutation);
  mutationRef.current = mutation;

  useEffect(() => () => controller.current?.abort(), []);

  const mutate = useCallback((request: ProblemAccountQueryRequest) => {
    controller.current?.abort();
    const nextController = new AbortController();
    controller.current = nextController;
    mutationRef.current.mutate({ request, controller: nextController });
  }, []);
  const reset = useCallback(() => {
    controller.current?.abort();
    mutationRef.current.reset();
  }, []);

  return { ...mutation, mutate, reset };
}
