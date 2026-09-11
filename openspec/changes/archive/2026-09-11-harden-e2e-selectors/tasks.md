## 1. Inventory

- [x] 1.1 Scan `web/e2e/**` and classify STABLE, FRAGILE, COPY-COUPLED, and STRUCTURE-COUPLED selectors.
- [x] 1.2 Identify Problems, Management/auth, and Jobs observability priority surfaces.
- [x] 1.3 Produce the minimum proposed `data-testid`/`data-*` selector list without changing code.

## 2. Follow-up implementation

- [ ] 2.1 Add reviewed page-root and navigation selectors.
- [ ] 2.2 Add reviewed business-entity selectors for Problems, occurrences, and Jobs.
- [ ] 2.3 Replace fragile selectors with explicit stable selectors and update affected E2E.
- [ ] 2.4 Add a thin selector guard if implementation review shows it is useful.
- [ ] 2.5 Run focused E2E validation and confirm no product behavior change.
