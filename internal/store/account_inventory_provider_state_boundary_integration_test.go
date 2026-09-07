package store_test

import (
	"context"
	"fmt"
	"testing"

	"strings"
)

func TestAccountInventoryProviderStateExactFreshnessBoundary(t *testing.T) {
	ctx := context.Background()
	database := newIsolatedJobDatabase(t)
	fixture := newLifecycleSchemaFixture(t, ctx, database)
	fixture.finalize(t, ctx, database, nil)
	// Fixture-only changes in an isolated DB. Both SPI statements run inside one
	// DO command: statement_timestamp is identical for the update and real safe
	// function read, avoiding wall-clock sleeps or substituting a freshness rule.
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states DISABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ age, want string }{{"15 minutes", "fresh"}, {"15 minutes 0.001 seconds", "stale"}} {
		t.Run(tc.want, func(t *testing.T) {
			sql := fmt.Sprintf(`DO $boundary$
   DECLARE actual text;
   BEGIN
    UPDATE public.account_inventory_provider_states
      SET           last_complete_at=statement_timestamp()-interval %s,
          source_observed_at=statement_timestamp()-interval %s
      WHERE instance_id=%s::uuid;
    SELECT snapshot_freshness INTO STRICT actual
      FROM public.control_query_account_inventory_provider_states_v1(%s::uuid)
      WHERE provider='openai';
    IF actual <> %s THEN RAISE EXCEPTION 'freshness boundary mismatch: %%',actual; END IF;
   END $boundary$;`, quoteTestLiteral(tc.age), quoteTestLiteral(tc.age), quoteTestLiteral(fixture.instanceID.String()), quoteTestLiteral(fixture.instanceID.String()), quoteTestLiteral(tc.want))
			if _, err := database.owner.Exec(ctx, sql); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := database.owner.Exec(ctx, `ALTER TABLE account_inventory_provider_states ENABLE TRIGGER account_inventory_provider_states_guard`); err != nil {
		t.Fatal(err)
	}
}

func quoteTestLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}
