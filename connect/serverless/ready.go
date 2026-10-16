package serverless

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

const readyPollInterval = 100 * time.Millisecond

// waitReady returns once the serve endpoint at appURL answers GET with an
// X-Inngest-SDK header.  it fails when ctx ends first.
func waitReady(ctx context.Context, client *http.Client, appURL string, keys signingKeys) error {
	for {
		err := probe(ctx, client, appURL, keys)
		if err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("app not ready: %w", err)
		case <-time.After(readyPollInterval):
		}
	}
}

func probe(ctx context.Context, client *http.Client, appURL string, keys signingKeys) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, appURL, nil)
	if err != nil {
		return err
	}
	// the GET is signed over an empty body.  without a valid signature, a Go
	// app in cloud mode answers 401 with no X-Inngest-SDK header.
	sig, err := keys.sign(ctx, nil)
	if err != nil {
		return err
	}
	if sig != "" {
		req.Header.Set(headerSignature, sig)
	}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()

	if resp.Header.Get(headerSDK) == "" {
		return fmt.Errorf("GET %s returned status %d with no %s header", appURL, resp.StatusCode, headerSDK)
	}
	return nil
}
