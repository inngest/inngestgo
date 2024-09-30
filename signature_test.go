package inngestgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	testBody        = []byte(`{"msg": "hey!  if you're reading this come work with us: careers@inngest.com"}`)
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

func TestValidateRequestSignature(t *testing.T) {
	ctx := context.Background()
	isDev := false

	t.Run("failures", func(t *testing.T) {
		t.Run("With an invalid sig it fails", func(t *testing.T) {
			ok, _, err := ValidateRequestSignature(ctx, "lol", testKey, "", testBody, isDev)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
		t.Run("With an invalid ts it fails", func(t *testing.T) {
			ok, _, err := ValidateRequestSignature(ctx, "t=what&s=yea", testKey, "", testBody, isDev)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid timestamp")
		})
		t.Run("With an expired ts it fails", func(t *testing.T) {
			ts := time.Now().Add(-1 * time.Hour).Unix()
			ok, _, err := ValidateRequestSignature(ctx, fmt.Sprintf("t=%d&s=yea", ts), testKey, "", testBody, isDev)
			require.False(t, ok)
			require.ErrorContains(t, err, "expired signature")
		})

		t.Run("with the wrong key it fails", func(t *testing.T) {
			at := time.Now()
			sig, _ := Sign(ctx, at, []byte(testKey), testBody)

			ok, _, err := ValidateRequestSignature(ctx, sig, "signkey-test-lolwtf", "", testBody, isDev)
			require.False(t, ok)
			require.ErrorContains(t, err, "invalid signature")
		})
	})

	t.Run("with the same key and within a reasonable time it succeeds", func(t *testing.T) {
		at := time.Now().Add(-5 * time.Second)
		sig, _ := Sign(ctx, at, []byte(testKey), testBody)

		ok, _, err := ValidateRequestSignature(ctx, sig, testKey, "", testBody, isDev)
		require.True(t, ok)
		require.NoError(t, err)
	})

	t.Run("successful response signature validation", func(t *testing.T) {
		at := time.Now().Add(-5 * time.Second)
		sig, _ := signWithoutJCS(at, []byte(testKey), testBody)

		ok, err := ValidateResponseSignature(ctx, sig, []byte(testKey), testBody)
		require.True(t, ok)
		require.NoError(t, err)
	})
}

func TestValidateResponseSignature(t *testing.T) {
	ctx := context.Background()

	t.Run("successful response signature validation", func(t *testing.T) {
		r := require.New(t)
		at := time.Now().Add(-5 * time.Second)
		sig, err := signWithoutJCS(at, []byte(testKey), testBody)
		r.NoError(err)

		ok, err := ValidateResponseSignature(ctx, sig, []byte(testKey), testBody)
		r.True(ok)
		r.NoError(err)
	})

	t.Run("successful response signature with JSON encoder", func(t *testing.T) {
		// Ensure that validation still works even after the JSON encoder adds a
		// trailing newline

		r := require.New(t)
		at := time.Now().Add(-5 * time.Second)

		body := map[string]string{"msg": "hi ☃"}
		bodyByt, err := json.Marshal(body)
		r.NoError(err)

		sig, err := signWithoutJCS(at, []byte(testKey), bodyByt)
		r.NoError(err)

		var buf bytes.Buffer
		encoder := json.NewEncoder(&buf)
		err = encoder.Encode(body)
		r.NoError(err)
		encodedBody := buf.Bytes()

		// Prove that the JSON encoder adds a trailing newline
		r.Equal(string(bodyByt)+"\n", string(encodedBody))

		ok, err := ValidateResponseSignature(ctx, sig, []byte(testKey), encodedBody)
		r.True(ok)
		r.NoError(err)
	})
}

func TestHashedSigning(t *testing.T) {
	actual, err := hashedSigningKey([]byte(testKey))
	require.NoError(t, err)
	require.Equal(t, actual, []byte("signkey-test-b2ed992186a5cb19f6668aade821f502c1d00970dfd0e35128d51bac4649916c"))
}
