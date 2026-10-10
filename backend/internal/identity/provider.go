// Package identity abstracts BVN/NIN verification behind a provider
// interface. Production must use a licensed identity provider
// (Mono, Dojah, YouVerify, Smile Identity): implement this interface with
// API calls + webhook handling, and swap FakeProvider in main.go — no other
// code changes. The fake is deterministic and documented for dev/test.
package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Result is the normalized provider verdict stored on the KYC document.
type Result struct {
	// Ref is the provider's reference for this check (audit trail).
	Ref string `json:"ref"`
	// Match reports names/DOB match the registry record.
	Match bool `json:"match"`
	// Reason is set when Match is false. It must stay generic enough to
	// show borrowers (the handler maps mismatch errors to client
	// messages) — never echo registry internals or PII here.
	Reason string `json:"reason,omitempty"`
	// ProfileMatch reports the submitted number equals the one on the
	// user's profile (fraud signal when false — someone else's document).
	ProfileMatch bool `json:"profileMatch"`
	CheckedAt    string `json:"checkedAt"`
}

type Provider interface {
	Name() string
	VerifyBVN(ctx context.Context, bvn, firstName, lastName string) (Result, error)
	VerifyNIN(ctx context.Context, nin, firstName, lastName string) (Result, error)
}

// FakeProvider simulates a registry with documented deterministic rules:
//   - BVN ending in "00"  → registry mismatch (wrong person).
//   - BVN ending in "99"  → provider outage (retryable error).
//   - NIN containing "999" → registry mismatch.
//   - NIN ending in "88"  → provider outage (retryable error).
//   - Anything else well-formed → match.
// A cancelled context aborts the check. Replace with a real HTTP client;
// keep Result as the boundary type so callers never change.
type FakeProvider struct{}

func (FakeProvider) Name() string { return "fake" }

func (FakeProvider) VerifyBVN(ctx context.Context, bvn, firstName, lastName string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(bvn) < 4 {
		return Result{}, errors.New("bvn too short")
	}
	ref := fmt.Sprintf("fake-bvn-%s-%d", bvn[len(bvn)-4:], time.Now().Unix())
	profile := Result{Ref: ref, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	switch {
	case strings.TrimSpace(firstName) == "" || strings.TrimSpace(lastName) == "":
		profile.Reason = "names required for registry match"
		return profile, nil
	case strings.HasSuffix(bvn, "99"):
		return Result{}, errors.New("fake provider unavailable (simulated outage)")
	case strings.HasSuffix(bvn, "00"):
		profile.Reason = "registry names do not match submitted names"
		return profile, nil
	default:
		profile.Match = true
		return profile, nil
	}
}

func (FakeProvider) VerifyNIN(ctx context.Context, nin, firstName, lastName string) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if len(nin) < 4 {
		return Result{}, errors.New("nin too short")
	}
	ref := fmt.Sprintf("fake-nin-%s-%d", nin[len(nin)-4:], time.Now().Unix())
	profile := Result{Ref: ref, CheckedAt: time.Now().UTC().Format(time.RFC3339)}
	switch {
	case strings.TrimSpace(firstName) == "" || strings.TrimSpace(lastName) == "":
		profile.Reason = "names required for registry match"
		return profile, nil
	case strings.HasSuffix(nin, "88"):
		return Result{}, errors.New("fake provider unavailable (simulated outage)")
	case strings.Contains(nin, "999"):
		profile.Reason = "no registry record for this NIN"
		return profile, nil
	default:
		profile.Match = true
		return profile, nil
	}
}
