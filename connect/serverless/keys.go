package serverless

import (
	"context"
	"fmt"
	"time"

	"github.com/inngest/inngestgo"
	"github.com/inngest/inngestgo/internal/signingkey"
)

// signingKeys are the plaintext signing keys.  the worker signs each request
// it sends to the app with the primary key, and the app checks it with the
// same key from its own env.
type signingKeys struct {
	primary  string
	fallback string
}

// sign returns the X-Inngest-Signature value for body.  it is empty when there
// is no key, which only the dev server accepts.
func (k signingKeys) sign(ctx context.Context, body []byte) (string, error) {
	if k.primary == "" {
		return "", nil
	}
	return inngestgo.Sign(ctx, time.Now(), []byte(k.primary), body)
}

// validResponse checks a response signature against either key.
func (k signingKeys) validResponse(ctx context.Context, sig string, body []byte) bool {
	for _, key := range []string{k.primary, k.fallback} {
		if key == "" {
			continue
		}
		if ok, _ := inngestgo.ValidateResponseSignature(ctx, sig, []byte(key), body); ok {
			return true
		}
	}
	return false
}

// hashed returns the bearer tokens that connect sends to the API.
func (k signingKeys) hashed() (primary, fallback []byte, err error) {
	if k.primary != "" {
		if primary, err = signingkey.Hash([]byte(k.primary)); err != nil {
			return nil, nil, fmt.Errorf("could not hash signing key: %w", err)
		}
	}
	if k.fallback != "" {
		if fallback, err = signingkey.Hash([]byte(k.fallback)); err != nil {
			return nil, nil, fmt.Errorf("could not hash fallback signing key: %w", err)
		}
	}
	return primary, fallback, nil
}
