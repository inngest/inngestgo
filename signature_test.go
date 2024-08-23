package inngestgo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	testBody        = []byte(`hey!  if you're reading this come work with us: careers@inngest.com`)
	testKey         = "signkey-test-12345678"
	testKeyFallback = "signkey-test-00000000"
)

func TestSign(t *testing.T) {
	ctx := context.Background()
	at := time.Now()

	t.Run("It produces the same sig with or without prefixes", func(t *testing.T) {
		keyA := []byte("signkey-test-12345678")
		keyB := []byte("signkey-prod-12345678")
		keyC := []byte("12345678")
		a, _ := Sign(ctx, at, keyA, testBody)
		b, _ := Sign(ctx, at, keyB, testBody)
		c, _ := Sign(ctx, at, keyC, testBody)
		require.Equal(t, a, b)
		require.Equal(t, a, c)
	})
}

func TestValidateSignature(t *testing.T) {
	ctx := context.Background()

	t.Run("failures", func(t *testing.T) {
		t.Run("With an invalid sig it fails", func(t *testing.T) {
			ok, _, err := ValidateSignature(ctx, "lol", testKey, "", testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
		t.Run("With an invalid ts it fails", func(t *testing.T) {
			ok, _, err := ValidateSignature(ctx, "t=what&s=yea", testKey, "", testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid timestamp")
		})
		t.Run("With an expired ts it fails", func(t *testing.T) {
			ts := time.Now().Add(-1 * time.Hour).Unix()
			ok, _, err := ValidateSignature(ctx, fmt.Sprintf("t=%d&s=yea", ts), testKey, "", testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "expired signature")
		})

		t.Run("with the wrong key it fails", func(t *testing.T) {
			at := time.Now()
			sig, _ := Sign(ctx, at, []byte(testKey), testBody)

			ok, _, err := ValidateSignature(ctx, sig, "signkey-test-lolwtf", "", testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
	})

	t.Run("with the same key and within a reasonable time it succeeds", func(t *testing.T) {
		at := time.Now().Add(-5 * time.Second)
		sig, _ := Sign(ctx, at, []byte(testKey), testBody)

		ok, _, err := ValidateSignature(ctx, sig, testKey, "", testBody)
		require.True(t, ok)
		require.NoError(t, err)
	})
}

func TestHashedSigning(t *testing.T) {
	actual, err := hashedSigningKey([]byte(testKey))
	require.NoError(t, err)
	require.Equal(t, actual, []byte("signkey-test-b2ed992186a5cb19f6668aade821f502c1d00970dfd0e35128d51bac4649916c"))
}
