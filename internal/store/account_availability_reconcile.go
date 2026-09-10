package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	var rows pgx.Rows
	if r.notifications == nil {
		rows, err = tx.Query(ctx, `SELECT node_id,account_key FROM public.control_reconcile_account_availability_v1($1,$2)`, nullableUUID(node), nullableText(account))
	} else {
		rows, err = tx.Query(ctx, `SELECT node_id,account_key,transitions FROM public.control_reconcile_account_availability_v2($1,$2)`, nullableUUID(node), nullableText(account))
	}
	if err != nil {
		return 0, node, account, err
	}
	count := 0
	var transitions []notificationTransition
	for rows.Next() {
		if r.notifications == nil {
			if err := rows.Scan(&node, &account); err != nil {
				rows.Close()
				return 0, node, account, err
			}
		} else {
			var encoded []byte
			if err := rows.Scan(&node, &account, &encoded); err != nil {
				rows.Close()
				return 0, node, account, err
			}
			var pageTransitions []notificationTransition
			if err := json.Unmarshal(encoded, &pageTransitions); err != nil {
				rows.Close()
				return 0, node, account, fmt.Errorf("store: decode availability notification transitions: %w", err)
			}
			transitions = append(transitions, pageTransitions...)
		}
		count++
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, node, account, err
	}
	if r.notifications != nil && len(transitions) > 0 {
		for _, transition := range transitions {
			if err := enqueueNotificationTx(ctx, tx, r.notifications, transition); err != nil {
				return 0, node, account, err
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return 0, node, account, err
	}
	return count, node, account, nil
}
