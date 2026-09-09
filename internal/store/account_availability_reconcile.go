package store

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Reconcile visits every key in bounded pages. Each page is an atomic
// SERIALIZABLE transaction, and a retry always starts with the same cursor.
func (r *AccountAvailabilityRepository) Reconcile(ctx context.Context) (int, error) {
	if r == nil || r.pool == nil {
		return 0, ErrAccountAvailabilityInconsistent
	}
	var node uuid.UUID
	account := ""
	total := 0
	for {
		var count int
		var nextNode uuid.UUID
		var nextAccount string
		var err error
		for attempt := 0; attempt < 8; attempt++ {
			count, nextNode, nextAccount, err = r.reconcileAvailabilityPage(ctx, node, account)
			if err == nil {
				break
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || (pgErr.Code != "40001" && pgErr.Code != "40P01") {
				return total, err
			}
			timer := time.NewTimer(time.Duration(attempt+1) * 10 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return total, ctx.Err()
			case <-timer.C:
			}
		}
		if err != nil {
			return total, err
		}
		total += count
		if count < 100 {
			return total, nil
		}
		node, account = nextNode, nextAccount
	}
}

func (r *AccountAvailabilityRepository) reconcileAvailabilityPage(ctx context.Context, node uuid.UUID, account string) (int, uuid.UUID, string, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return 0, node, account, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `SELECT node_id,account_key FROM public.control_reconcile_account_availability_v1($1,$2)`, nullableUUID(node), nullableText(account))
	if err != nil {
		return 0, node, account, err
	}
	count := 0
	for rows.Next() {
		if err := rows.Scan(&node, &account); err != nil {
			rows.Close()
			return 0, node, account, err
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, node, account, err
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, node, account, err
	}
	return count, node, account, nil
}
