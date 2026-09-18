package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	generated "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

const adminCommandDomainAsset = "asset_admin"

var ErrCommandRegistryInconsistent = errors.New("store: admin command registry inconsistent")

func lockAdminCommand(ctx context.Context, tx pgx.Tx, commandID uuid.UUID) error {
	sum := sha256.Sum256(commandID[:])
	_, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock($1)`, int64(binary.BigEndian.Uint64(sum[:8])))
	return err
}

type adminCommandReservation struct {
	ActorAdminID                uuid.UUID
	CommandDomain               string
	CommandKind                 string
	IntentEncodingVersion       int16
	CanonicalIntentHash         []byte
	SecretFingerprintKeyVersion *int16
}

func (r adminCommandReservation) matchesReceipt(actor uuid.UUID, kind string, encoding int16, hash []byte, keyVersion *int16) bool {
	return r.ActorAdminID == actor && r.CommandDomain == adminCommandDomainAsset &&
		r.CommandKind == kind && r.IntentEncodingVersion == encoding &&
		hmac.Equal(r.CanonicalIntentHash, hash) && equalOptionalInt16(r.SecretFingerprintKeyVersion, keyVersion)
}

func equalOptionalInt16(left, right *int16) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func lookupAdminCommandReservation(ctx context.Context, tx pgx.Tx, commandID uuid.UUID) (adminCommandReservation, bool, error) {
	row, err := generated.New(tx).GetAdminCommandReservation(ctx, nullableUUID(commandID))
	if errors.Is(err, pgx.ErrNoRows) {
		return adminCommandReservation{}, false, nil
	}
	if err != nil {
		return adminCommandReservation{}, false, err
	}
	if !row.ActorAdminID.Valid {
		return adminCommandReservation{}, false, ErrCommandRegistryInconsistent
	}
	reservation := adminCommandReservation{
		ActorAdminID:          uuid.UUID(row.ActorAdminID.Bytes),
		CommandDomain:         row.CommandDomain,
		CommandKind:           row.CommandKind,
		IntentEncodingVersion: row.IntentEncodingVersion,
		CanonicalIntentHash:   append([]byte(nil), row.CanonicalIntentHash...),
	}
	if row.SecretFingerprintKeyVersion.Valid {
		value := row.SecretFingerprintKeyVersion.Int16
		reservation.SecretFingerprintKeyVersion = &value
	}
	return reservation, true, nil
}

func reserveAdminCommand(ctx context.Context, tx pgx.Tx, commandID, actorAdminID uuid.UUID, kind string, encodingVersion int16, intentHash []byte, keyVersion *int16) error {
	_, err := generated.New(tx).ReserveAdminCommand(ctx, generated.ReserveAdminCommandParams{
		CommandID:                   nullableUUID(commandID),
		ActorAdminID:                nullableUUID(actorAdminID),
		CommandDomain:               adminCommandDomainAsset,
		CommandKind:                 kind,
		IntentEncodingVersion:       encodingVersion,
		CanonicalIntentHash:         intentHash,
		SecretFingerprintKeyVersion: nullableInt2(keyVersion),
	})
	return err
}

func insertControlledAssetAdminCommandReceipt(ctx context.Context, tx pgx.Tx, commandID, actorAdminID uuid.UUID, kind string, encodingVersion int16, intentHash, result []byte, status int, committedAt *time.Time, keyVersion *int16) error {
	var timestamp pgtype.Timestamptz
	if committedAt != nil {
		timestamp = pgtype.Timestamptz{Time: *committedAt, Valid: true}
	}
	return generated.New(tx).InsertControlledAssetAdminCommandReceipt(ctx, generated.InsertControlledAssetAdminCommandReceiptParams{
		CommandID:                   nullableUUID(commandID),
		CommandKind:                 kind,
		IntentEncodingVersion:       encodingVersion,
		CanonicalIntentHash:         intentHash,
		SanitizedResult:             result,
		ResponseStatus:              int16(status),
		ActorAdminID:                nullableUUID(actorAdminID),
		CommittedAt:                 timestamp,
		SecretFingerprintKeyVersion: nullableInt2(keyVersion),
	})
}

func nullableInt2(value *int16) pgtype.Int2 {
	if value == nil {
		return pgtype.Int2{}
	}
	return pgtype.Int2{Int16: *value, Valid: true}
}
