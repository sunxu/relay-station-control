package auth

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	store "github.com/sunxu/relay-station-control/internal/store/sqlc"
)

func (s *Service) lookupChallenge(ctx context.Context, q *store.Queries, token string) (store.ControlAuthChallenge, Digest, error) {
	digests, err := s.digestCandidates(DomainChallengeDigest, token)
	if err != nil {
		return store.ControlAuthChallenge{}, Digest{}, err
	}
	for _, digest := range digests {
		row, lookupErr := q.GetActiveAuthChallengeByDigest(ctx, store.GetActiveAuthChallengeByDigestParams{KeyVersion: int32(digest.KeyVersion), TokenDigest: digest.Sum[:]})
		if lookupErr == nil {
			return row, digest, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return store.ControlAuthChallenge{}, Digest{}, lookupErr
		}
	}
	// Preserve a valid, keyed fallback subject for rate-limit accounting even
	// when the challenge is expired, consumed, revoked, or otherwise unknown.
	// Returning a zero key version would violate the persisted failure schema
	// and incorrectly turn an authentication rejection into a 503.
	if len(digests) > 0 {
		return store.ControlAuthChallenge{}, digests[0], pgx.ErrNoRows
	}
	return store.ControlAuthChallenge{}, Digest{}, pgx.ErrNoRows
}

func (s *Service) lookupSession(ctx context.Context, token string) (store.GetActiveAdminSessionByDigestRow, error) {
	digests, err := s.digestCandidates(DomainSessionDigest, token)
	if err != nil {
		return store.GetActiveAdminSessionByDigestRow{}, err
	}
	for _, digest := range digests {
		row, lookupErr := s.queries.GetActiveAdminSessionByDigest(ctx, store.GetActiveAdminSessionByDigestParams{KeyVersion: int32(digest.KeyVersion), TokenDigest: digest.Sum[:]})
		if lookupErr == nil {
			return row, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return store.GetActiveAdminSessionByDigestRow{}, lookupErr
		}
	}
	return store.GetActiveAdminSessionByDigestRow{}, pgx.ErrNoRows
}

func (s *Service) lockActivation(ctx context.Context, q *store.Queries, token string) (store.ControlAdminActivationToken, Digest, error) {
	digests, err := s.digestCandidates(DomainActivationDigest, token)
	if err != nil {
		return store.ControlAdminActivationToken{}, Digest{}, err
	}
	for _, digest := range digests {
		row, lookupErr := q.LockActiveActivationTokenByDigest(ctx, store.LockActiveActivationTokenByDigestParams{KeyVersion: int32(digest.KeyVersion), TokenDigest: digest.Sum[:]})
		if lookupErr == nil {
			return row, digest, nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return store.ControlAdminActivationToken{}, Digest{}, lookupErr
		}
	}
	return store.ControlAdminActivationToken{}, Digest{}, pgx.ErrNoRows
}

func (s *Service) consumeRecovery(ctx context.Context, q *store.Queries, adminID pgtype.UUID, code string) error {
	digests, err := s.digestCandidates(DomainRecoveryCodeDigest, code)
	if err != nil {
		return ErrAuthentication
	}
	for _, digest := range digests {
		row, consumeErr := q.ConsumeRecoveryCode(ctx, store.ConsumeRecoveryCodeParams{AdminID: adminID, KeyVersion: int32(digest.KeyVersion), CodeDigest: digest.Sum[:]})
		if consumeErr == nil {
			if row.AdminID == adminID {
				return nil
			}
			return ErrAuthentication
		}
		if !errors.Is(consumeErr, pgx.ErrNoRows) {
			return consumeErr
		}
	}
	return ErrAuthentication
}
