package main

import (
	"context"
	"fmt"
	"time"

	"lending-app/backend/internal/domain/entities"
	"lending-app/backend/internal/repositories"
	"lending-app/backend/pkg/crypto"
)

// rekeyResult summarizes a re-encryption run. Only counts are logged —
// never plaintext, ciphertext, or key material.
type rekeyResult struct {
	scanned   int
	rewritten int
	skipped   int
	failed    int
}

// runRekey rewrites every user's PII envelope to the PRIMARY key version and
// refreshes HMACs. Idempotent: rows already at the primary version are
// skipped (unless forceHMAC for HMAC-only rotation). Safe to re-run; a second
// operator gets save-skipped via advisory lock.
//
// dryRun reports what WOULD change without writing.
func runRekey(ctx context.Context, app *App, enc *crypto.EncryptionService, dryRun, forceHMAC bool) (*rekeyResult, error) {
	lockCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	got, unlock, err := repositories.TryAdvisoryLock(lockCtx, app.db, repositories.LockRekey)
	if err != nil {
		return nil, err
	}
	if !got {
		return nil, fmt.Errorf("rekey already running elsewhere")
	}
	defer unlock()

	userRepo := repositories.NewUserRepository(app.db, enc)
	res := &rekeyResult{}
	const pageSize = 500
	offset := 0
	for {
		pageCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		users, total, err := userRepo.List(pageCtx, pageSize, offset)
		cancel()
		if err != nil {
			return res, fmt.Errorf("list users offset %d: %w", offset, err)
		}
		for _, u := range users {
			res.scanned++
			// List→scan already decrypted every field through the key
			// ring; reaching here proves readability under old+new keys.
			if !forceHMAC && atPrimaryVersion(u, enc.PrimaryVersion()) {
				res.skipped++
				continue
			}
			if dryRun {
				res.rewritten++
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := userRepo.UpdatePII(wctx, u)
			cancel()
			if err != nil {
				res.failed++
				app.logger.Error("rekey row failed", "userID", u.ID.String())
				continue
			}
			res.rewritten++
		}
		offset += len(users)
		if offset >= total || len(users) == 0 {
			break
		}
		// Gentle pacing: never saturate the pool on large tables.
		time.Sleep(50 * time.Millisecond)
	}
	return res, nil
}

// atPrimaryVersion reports whether every stored PII envelope already uses
// the primary key ID.
func atPrimaryVersion(u *entities.User, id string) bool {
	fields := []entities.EncryptedString{
		u.Email, u.FirstName, u.LastName, u.Phone, u.BVN, u.NIN,
		u.Address, u.City, u.State,
	}
	for _, f := range fields {
		if f.Data == "" {
			continue
		}
		if crypto.VersionOf(f.Data) != id {
			return false
		}
	}
	return true
}
