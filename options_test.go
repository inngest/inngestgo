package inngestgo

import (
	"fmt"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestServeOriginOverride(t *testing.T) {
	t.Run("no override", func(t *testing.T) {
		r := require.New(t)
		hOpts := handlerOpts{}
		r.Nil(serveOriginOverride(hOpts))
	})

	t.Run("ServeOrigin has highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_HOST", "https://env-var.com")
		url, err := url.Parse("https://URL.com/baz/qux")
		r.NoError(err)
		hOpts := handlerOpts{
			ServeOrigin: StrPtr("https://ServeOrigin.com/foo/bar"),
			URL:         url,
		}
		r.Equal(*hOpts.ServeOrigin, *serveOriginOverride(hOpts))
	})

	t.Run("URL has second highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_HOST", "https://env-var.com")
		url, err := url.Parse("https://URL.com/baz/qux")
		r.NoError(err)
		hOpts := handlerOpts{URL: url}
		r.Equal(fmt.Sprintf("%s://%s", url.Scheme, url.Host), *serveOriginOverride(hOpts))
	})

	t.Run("INNGEST_SERVE_HOST has lowest precedence", func(t *testing.T) {
		r := require.New(t)
		t.Setenv("INNGEST_SERVE_HOST", "https://example.com")
		hOpts := handlerOpts{}
		r.Equal("https://example.com", *serveOriginOverride(hOpts))
	})
}

func TestServePathOverride(t *testing.T) {
	t.Run("no override", func(t *testing.T) {
		r := require.New(t)
		hOpts := handlerOpts{}
		r.Nil(servePathOverride(hOpts))
	})

	t.Run("ServePath has highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_PATH", "/env/var")
		url, err := url.Parse("https://URL.com/url/var")
		r.NoError(err)
		hOpts := handlerOpts{
			ServePath: StrPtr("/serve-path/var"),
			URL:       url,
		}
		r.Equal(*hOpts.ServePath, *servePathOverride(hOpts))
	})

	t.Run("URL has second highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_PATH", "/env/var")
		url, err := url.Parse("https://URL.com/url/var")
		r.NoError(err)
		hOpts := handlerOpts{URL: url}
		r.Equal(url.Path, *servePathOverride(hOpts))
	})

	t.Run("INNGEST_SERVE_PATH has lowest precedence", func(t *testing.T) {
		r := require.New(t)
		t.Setenv("INNGEST_SERVE_PATH", "/env/var")
		hOpts := handlerOpts{}
		r.Equal("/env/var", *servePathOverride(hOpts))
	})
}

func TestOverrideURL(t *testing.T) {
	originalURL, err := url.Parse("https://original.com/original")
	require.NoError(t, err)

	t.Run("no override", func(t *testing.T) {
		r := require.New(t)
		hOpts := handlerOpts{}
		overrideURL, err := overrideURL(originalURL, hOpts)
		r.NoError(err)
		r.Equal(originalURL, overrideURL)
	})

	t.Run("ServeOrigin/ServePath fields have highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_HOST", "https://env.com")
		t.Setenv("INNGEST_SERVE_PATH", "/env")
		r.NoError(err)
		hOpts := handlerOpts{
			ServeOrigin: StrPtr("https://serve-field.com/serve-field"),
			URL:         urlMustParse(t, "https://url-field.com/url-field"),
		}
		r.Equal(*hOpts.ServeOrigin, *serveOriginOverride(hOpts))
	})

	t.Run("URL field has second highest precedence", func(t *testing.T) {
		r := require.New(t)

		t.Setenv("INNGEST_SERVE_HOST", "https://env.com")
		t.Setenv("INNGEST_SERVE_PATH", "/env")
		r.NoError(err)
		hOpts := handlerOpts{
			URL: urlMustParse(t, "https://url-field.com/url-field"),
		}
		overrideURL, err := overrideURL(hOpts.URL, hOpts)
		r.NoError(err)
		r.Equal("https://url-field.com/url-field", overrideURL.String())
	})

	t.Run("env vars have lowest precedence", func(t *testing.T) {
		r := require.New(t)
		t.Setenv("INNGEST_SERVE_HOST", "https://env.com")
		t.Setenv("INNGEST_SERVE_PATH", "/env")
		hOpts := handlerOpts{}
		overrideURL, err := overrideURL(originalURL, hOpts)
		r.NoError(err)
		r.Equal("https://env.com/env", overrideURL.String())
	})
}

func urlMustParse(t *testing.T, s string) *url.URL {
	url, err := url.Parse(s)
	require.NoError(t, err)
	return url
}
