package inngestgo

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	testBody = []byte(`hey!  if you're reading this come work with us: careers@inngest.com`)
	testKey  = []byte("signkey-test-12345678")
)

func TestSign(t *testing.T) {
	ctx := context.Background()
	at := time.Now()

	t.Run("It produces the same sig with or without prefixes", func(t *testing.T) {
		keyA := []byte("signkey-test-12345678")
		keyB := []byte("signkey-prod-12345678")
		keyC := []byte("12345678")
		a := Sign(ctx, at, keyA, testBody)
		b := Sign(ctx, at, keyB, testBody)
		c := Sign(ctx, at, keyC, testBody)
		require.Equal(t, a, b)
		require.Equal(t, a, c)
	})
}

func TestValidateSignature(t *testing.T) {
	ctx := context.Background()

	t.Run("failures", func(t *testing.T) {
		t.Run("With an invalid sig it fails", func(t *testing.T) {
			ok, err := ValidateSignature(ctx, "lol", testKey, testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
		t.Run("With an invalid ts it fails", func(t *testing.T) {
			ok, err := ValidateSignature(ctx, "t=what&s=yea", testKey, testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid timestamp")
		})
		t.Run("With an expired ts it fails", func(t *testing.T) {
			ts := time.Now().Add(-1 * time.Hour).Unix()
			ok, err := ValidateSignature(ctx, fmt.Sprintf("t=%d&s=yea", ts), testKey, testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "expired signature")
		})

		t.Run("with the wrong key it fails", func(t *testing.T) {
			at := time.Now()
			sig := Sign(ctx, at, testKey, testBody)

			ok, err := ValidateSignature(ctx, sig, []byte("signkey-test-lolwtf"), testBody)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
	})

	t.Run("with the same key and within a reasonable time it succeeds", func(t *testing.T) {
		at := time.Now().Add(-5 * time.Second)
		sig := Sign(ctx, at, testKey, testBody)

		ok, err := ValidateSignature(ctx, sig, testKey, testBody)
		require.True(t, ok)
		require.NoError(t, err)
	})
}

func TestHashedSigning(t *testing.T) {
	actual, err := hashedSigningKey(testKey)
	require.NoError(t, err)
	require.Equal(t, actual, []byte("signkey-test-b2ed992186a5cb19f6668aade821f502c1d00970dfd0e35128d51bac4649916c"))
}
