package assets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPublicImageDownloaderDownloadsSniffedPNGToSafePath(t *testing.T) {
	now := time.Date(2026, 7, 2, 12, 0, 0, 0, time.UTC)
	body := tinyDownloadPNG(t)
	var gotRequest *http.Request
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		gotRequest = req
		return downloadHTTPResponse(req, http.StatusOK, nil, body), nil
	}), now)

	result, err := downloader.Download(context.Background(), DownloadRequest{
		URL:       "https://cdn.example/images/avatar.png?scale=2",
		MediaType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if gotRequest == nil {
		t.Fatal("HTTP transport was not called")
	}
	if got := gotRequest.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization header = %q, want empty", got)
	}
	if got := gotRequest.Header.Get("Cookie"); got != "" {
		t.Fatalf("Cookie header = %q, want empty", got)
	}
	if result.MediaType != "image/png" {
		t.Fatalf("result.MediaType = %q, want image/png", result.MediaType)
	}
	if !result.FetchedAt.Equal(now) {
		t.Fatalf("result.FetchedAt = %s, want %s", result.FetchedAt, now)
	}
	got, err := os.ReadFile(result.Path)
	if err != nil {
		t.Fatalf("ReadFile downloaded asset returned error: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("downloaded bytes = %d bytes, want %d", len(got), len(body))
	}
	assertSafeDownloadPath(t, result.Path)
}

func TestPublicImageDownloaderDownloadsSupportedSniffedFormats(t *testing.T) {
	tests := []struct {
		name      string
		body      []byte
		wantMedia string
	}{
		{name: "png", body: tinyDownloadPNG(t), wantMedia: "image/png"},
		{name: "jpeg", body: tinyDownloadJPEG(t), wantMedia: "image/jpeg"},
		{name: "gif", body: tinyDownloadGIF(t), wantMedia: "image/gif"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return downloadHTTPResponse(req, http.StatusOK, map[string]string{
					"Content-Type": "application/octet-stream",
				}, tt.body), nil
			}), time.Now())

			result, err := downloader.Download(context.Background(), DownloadRequest{
				URL:       "https://cdn.example/images/asset.bin",
				MediaType: "application/octet-stream",
			})
			if err != nil {
				t.Fatalf("Download returned error: %v", err)
			}
			if result.MediaType != tt.wantMedia {
				t.Fatalf("result.MediaType = %q, want %s", result.MediaType, tt.wantMedia)
			}
			got, err := os.ReadFile(result.Path)
			if err != nil {
				t.Fatalf("ReadFile downloaded asset returned error: %v", err)
			}
			if !bytes.Equal(got, tt.body) {
				t.Fatalf("downloaded bytes = %d bytes, want %d", len(got), len(tt.body))
			}
			assertSafeDownloadPath(t, result.Path)
		})
	}
}

func TestPublicImageDownloaderFollowsAcceptedRedirect(t *testing.T) {
	body := tinyDownloadPNG(t)
	var requested []string
	downloader := testPublicImageDownloaderWithResolver(t, fakeHostResolver{
		"cdn.example":    {ipAddr("93.184.216.34")},
		"assets.example": {ipAddr("142.250.191.110")},
	}, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requested = append(requested, req.URL.String())
		switch req.URL.Hostname() {
		case "cdn.example":
			return downloadHTTPResponse(req, http.StatusFound, map[string]string{
				"Location": "https://assets.example/final/emote.gif",
			}, nil), nil
		case "assets.example":
			return downloadHTTPResponse(req, http.StatusOK, map[string]string{
				"Content-Type": "image/gif",
			}, body), nil
		default:
			t.Fatalf("unexpected redirect host %q", req.URL.Hostname())
			return nil, nil
		}
	}), time.Now())

	result, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/start/emote.gif"})
	if err != nil {
		t.Fatalf("Download returned error: %v", err)
	}
	if result.MediaType != "image/png" {
		t.Fatalf("result.MediaType = %q, want sniffed image/png", result.MediaType)
	}
	if len(requested) != 2 {
		t.Fatalf("requested URLs = %#v, want initial plus redirect", requested)
	}
	assertSafeDownloadPath(t, result.Path)
}

func TestPublicImageDownloaderRejectsUnsafeRedirect(t *testing.T) {
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return downloadHTTPResponse(req, http.StatusFound, map[string]string{
			"Location": "http://127.0.0.1/private.png",
		}, nil), nil
	}), time.Now())

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/start.png"})
	if !errors.Is(err, ErrAssetDownloadUnsafeSource) {
		t.Fatalf("Download error = %v, want ErrAssetDownloadUnsafeSource", err)
	}
	assertNoSecretText(t, err.Error(), "http://127.0.0.1", "private.png")
}

func TestPublicImageDownloaderDefaultDialUsesValidatedResolvedIP(t *testing.T) {
	var dialed []string
	var serverConns []net.Conn
	downloader := NewPublicImageDownloader(PublicImageDownloaderOptions{
		Resolver: fakeHostResolver{"cdn.example": {ipAddr("93.184.216.34")}},
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			dialed = append(dialed, address)
			client, server := net.Pipe()
			serverConns = append(serverConns, server)
			return client, nil
		},
	})
	defer func() {
		for _, conn := range serverConns {
			_ = conn.Close()
		}
	}()

	conn, err := downloader.publicDialContext(context.Background(), "tcp", "cdn.example:443")
	if err != nil {
		t.Fatalf("publicDialContext returned error: %v", err)
	}
	_ = conn.Close()
	if len(dialed) != 1 || dialed[0] != "93.184.216.34:443" {
		t.Fatalf("dialed addresses = %#v, want validated resolved IP", dialed)
	}
}

func TestPublicImageDownloaderDefaultDialRejectsPrivateResolvedIP(t *testing.T) {
	called := false
	downloader := NewPublicImageDownloader(PublicImageDownloaderOptions{
		Resolver: fakeHostResolver{"cdn.example": {ipAddr("10.0.0.4")}},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			called = true
			return nil, errors.New("unexpected dial")
		},
	})

	_, err := downloader.publicDialContext(context.Background(), "tcp", "cdn.example:443")
	if !errors.Is(err, ErrAssetDownloadUnsafeSource) {
		t.Fatalf("publicDialContext error = %v, want ErrAssetDownloadUnsafeSource", err)
	}
	if called {
		t.Fatal("DialContext was called for private resolved IP")
	}
	assertNoSecretText(t, err.Error(), "cdn.example:443", "10.0.0.4")
}

func TestPublicImageDownloaderDoesNotUseCustomHTTPClientTransport(t *testing.T) {
	called := false
	downloader := NewPublicImageDownloader(PublicImageDownloaderOptions{
		HTTPClient: &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				return downloadHTTPResponse(req, http.StatusOK, nil, tinyDownloadPNG(t)), nil
			}),
		},
		Resolver: fakeHostResolver{"cdn.example": {ipAddr("93.184.216.34")}},
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("dial stopped")
		},
		DownloadDir: t.TempDir(),
	})

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "http://cdn.example/avatar.png"})
	if !errors.Is(err, ErrAssetDownloadFailed) {
		t.Fatalf("Download error = %v, want ErrAssetDownloadFailed", err)
	}
	if called {
		t.Fatal("custom HTTPClient transport was used")
	}
}

func TestPublicImageDownloaderHonorsContextCancellationBeforeNetwork(t *testing.T) {
	called := false
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		called = true
		return downloadHTTPResponse(req, http.StatusOK, nil, tinyDownloadPNG(t)), nil
	}), time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := downloader.Download(ctx, DownloadRequest{URL: "https://cdn.example/canceled.png"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Download error = %v, want context.Canceled", err)
	}
	if called {
		t.Fatal("HTTP transport was called after context cancellation")
	}
}

func TestPublicImageDownloaderHonorsHTTPClientTimeout(t *testing.T) {
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done()
		return nil, req.Context().Err()
	}), time.Now())
	downloader.Options.HTTPClient.Timeout = time.Nanosecond

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/slow.png"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Download error = %v, want context.DeadlineExceeded", err)
	}
	assertNoSecretText(t, err.Error(), "https://cdn.example/slow.png")
}

func TestPublicImageDownloaderRejectsOversizeResponses(t *testing.T) {
	tests := []struct {
		name          string
		contentLength int64
	}{
		{name: "declared content length", contentLength: 5},
		{name: "unknown content length", contentLength: -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				resp := downloadHTTPResponse(req, http.StatusOK, map[string]string{
					"Content-Type": "image/png",
				}, []byte("12345"))
				resp.ContentLength = tt.contentLength
				return resp, nil
			}), time.Now())
			downloader.Options.DownloadDir = dir
			downloader.Options.MaxBytes = 4

			_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/large.png"})
			if !errors.Is(err, ErrAssetDownloadTooLarge) {
				t.Fatalf("Download error = %v, want ErrAssetDownloadTooLarge", err)
			}
			if entries := readDirNames(t, dir); len(entries) != 0 {
				t.Fatalf("download dir entries after oversize = %#v, want none", entries)
			}
		})
	}
}

func TestPublicImageDownloaderRejectsUnsupportedContent(t *testing.T) {
	tests := []struct {
		name        string
		headers     map[string]string
		body        []byte
		metadata    string
		notLeaked   []string
		wantWritten bool
	}{
		{
			name:      "html response",
			headers:   map[string]string{"Content-Type": "text/html; charset=utf-8"},
			body:      []byte("<html>access_token=secret</html>"),
			metadata:  "image/png",
			notLeaked: []string{"access_token=secret", "<html>"},
		},
		{
			name:      "missing content type with text body",
			body:      []byte("not an image even if metadata says png"),
			metadata:  "image/png",
			notLeaked: []string{"not an image"},
		},
		{
			name:      "image header with html body",
			headers:   map[string]string{"Content-Type": "image/png"},
			body:      []byte("<html>not a png</html>"),
			metadata:  "image/png",
			notLeaked: []string{"<html>"},
		},
		{
			name:      "image header with generic binary body",
			headers:   map[string]string{"Content-Type": "image/png"},
			body:      []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05},
			metadata:  "image/png",
			notLeaked: []string{"image/png"},
		},
		{
			name:      "unsupported webp",
			headers:   map[string]string{"Content-Type": "image/webp"},
			body:      []byte("RIFFxxxxWEBPVP8 "),
			metadata:  "image/webp",
			notLeaked: []string{"RIFF"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return downloadHTTPResponse(req, http.StatusOK, tt.headers, tt.body), nil
			}), time.Now())
			downloader.Options.DownloadDir = dir

			_, err := downloader.Download(context.Background(), DownloadRequest{
				URL:       "https://cdn.example/unsupported",
				MediaType: tt.metadata,
			})
			if !errors.Is(err, ErrAssetDownloadUnsupportedMediaType) {
				t.Fatalf("Download error = %v, want ErrAssetDownloadUnsupportedMediaType", err)
			}
			assertNoSecretText(t, err.Error(), tt.notLeaked...)
			if entries := readDirNames(t, dir); len(entries) != 0 {
				t.Fatalf("download dir entries after unsupported content = %#v, want none", entries)
			}
		})
	}
}

func TestPublicImageDownloaderRejectsBadStatusWithoutBodyLeak(t *testing.T) {
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return downloadHTTPResponse(req, http.StatusForbidden, nil, []byte("client_secret=secret")), nil
	}), time.Now())

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/forbidden.png"})
	if !errors.Is(err, ErrAssetDownloadBadStatus) {
		t.Fatalf("Download error = %v, want ErrAssetDownloadBadStatus", err)
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Fatalf("Download error = %q, want status code", err.Error())
	}
	assertNoSecretText(t, err.Error(), "client_secret=secret", "https://cdn.example/forbidden.png")
}

func TestPublicImageDownloaderRejectsInvalidAndUnsafeURLs(t *testing.T) {
	tests := []struct {
		name     string
		source   string
		resolver fakeHostResolver
		want     error
		secrets  []string
	}{
		{
			name:   "missing source",
			source: "",
			want:   ErrAssetDownloadInvalidURL,
		},
		{
			name:   "file scheme",
			source: "file:///tmp/avatar.png",
			want:   ErrAssetDownloadInvalidURL,
		},
		{
			name:   "ftp scheme",
			source: "ftp://cdn.example/avatar.png",
			want:   ErrAssetDownloadInvalidURL,
		},
		{
			name:    "userinfo",
			source:  "https://viewer:secret@cdn.example/avatar.png",
			want:    ErrAssetDownloadUnsafeSource,
			secrets: []string{"viewer:secret"},
		},
		{
			name:    "credential query",
			source:  "https://cdn.example/avatar.png?access_token=secret-token",
			want:    ErrAssetDownloadUnsafeSource,
			secrets: []string{"access_token=secret-token", "secret-token"},
		},
		{
			name:    "escaped credential query",
			source:  "https://cdn.example/avatar.png?client%5Fsecret=secret-token",
			want:    ErrAssetDownloadUnsafeSource,
			secrets: []string{"client%5Fsecret=secret-token", "secret-token"},
		},
		{
			name:    "cookie query",
			source:  "https://cdn.example/avatar.png?cookie=session-token",
			want:    ErrAssetDownloadUnsafeSource,
			secrets: []string{"cookie=session-token", "session-token"},
		},
		{
			name:    "generic token query",
			source:  "https://cdn.example/avatar.png?token=secret-token",
			want:    ErrAssetDownloadUnsafeSource,
			secrets: []string{"token=secret-token", "secret-token"},
		},
		{
			name:   "loopback IP",
			source: "http://127.0.0.1/avatar.png",
			want:   ErrAssetDownloadUnsafeSource,
		},
		{
			name:   "carrier grade NAT IP",
			source: "http://100.64.0.1/avatar.png",
			want:   ErrAssetDownloadUnsafeSource,
		},
		{
			name:   "documentation IP",
			source: "https://192.0.2.10/avatar.png",
			want:   ErrAssetDownloadUnsafeSource,
		},
		{
			name:   "unique local IPv6",
			source: "https://[fc00::1]/avatar.png",
			want:   ErrAssetDownloadUnsafeSource,
		},
		{
			name:   "localhost",
			source: "http://localhost/avatar.png",
			want:   ErrAssetDownloadUnsafeSource,
		},
		{
			name:   "malformed host",
			source: "https://[::1/avatar.png",
			want:   ErrAssetDownloadInvalidURL,
		},
		{
			name:   "out of range port",
			source: "https://cdn.example:99999/avatar.png",
			want:   ErrAssetDownloadInvalidURL,
		},
		{
			name:     "private resolved host",
			source:   "https://private.example/avatar.png",
			resolver: fakeHostResolver{"private.example": {ipAddr("10.0.0.4")}},
			want:     ErrAssetDownloadUnsafeSource,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			resolver := tt.resolver
			if resolver == nil {
				resolver = fakeHostResolver{"cdn.example": {ipAddr("93.184.216.34")}}
			}
			downloader := testPublicImageDownloaderWithResolver(t, resolver, roundTripFunc(func(req *http.Request) (*http.Response, error) {
				called = true
				return downloadHTTPResponse(req, http.StatusOK, nil, tinyDownloadPNG(t)), nil
			}), time.Now())

			_, err := downloader.Download(context.Background(), DownloadRequest{URL: tt.source})
			if !errors.Is(err, tt.want) {
				t.Fatalf("Download error = %v, want %v", err, tt.want)
			}
			if called {
				t.Fatal("HTTP transport was called for invalid or unsafe URL")
			}
			assertNoSecretText(t, err.Error(), append(tt.secrets, tt.source)...)
		})
	}
}

func TestPublicImageDownloaderRedactsTransportErrors(t *testing.T) {
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial %s failed with Authorization: Bearer secret-token Cookie: session=client_secret=secret", req.URL.String())
	}), time.Now())

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/avatar.png"})
	if !errors.Is(err, ErrAssetDownloadFailed) {
		t.Fatalf("Download error = %v, want ErrAssetDownloadFailed", err)
	}
	assertNoSecretText(t, err.Error(),
		"https://cdn.example/avatar.png",
		"Authorization",
		"Bearer secret-token",
		"Cookie",
		"client_secret=secret",
	)
}

func TestPublicImageDownloaderRejectsUnsafeDownloadDir(t *testing.T) {
	downloader := testPublicImageDownloader(t, roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return downloadHTTPResponse(req, http.StatusOK, nil, tinyDownloadPNG(t)), nil
	}), time.Now())
	downloader.Options.DownloadDir = filepath.Join(t.TempDir(), "client_secret=secret")

	_, err := downloader.Download(context.Background(), DownloadRequest{URL: "https://cdn.example/avatar.png"})
	if !errors.Is(err, ErrAssetDownloadUnsafePath) {
		t.Fatalf("Download error = %v, want ErrAssetDownloadUnsafePath", err)
	}
	assertNoSecretText(t, err.Error(), "client_secret=secret")
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type fakeHostResolver map[string][]net.IPAddr

func (r fakeHostResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if addrs, ok := r[strings.ToLower(strings.TrimSuffix(host, "."))]; ok {
		return addrs, nil
	}
	return nil, &net.DNSError{IsNotFound: true}
}

func testPublicImageDownloader(t *testing.T, transport http.RoundTripper, now time.Time) *PublicImageDownloader {
	t.Helper()
	return testPublicImageDownloaderWithResolver(t, fakeHostResolver{
		"cdn.example": {ipAddr("93.184.216.34")},
	}, transport, now)
}

func testPublicImageDownloaderWithResolver(t *testing.T, resolver fakeHostResolver, transport http.RoundTripper, now time.Time) *PublicImageDownloader {
	t.Helper()
	return NewPublicImageDownloader(PublicImageDownloaderOptions{
		HTTPClient:  &http.Client{},
		Resolver:    resolver,
		transport:   transport,
		DownloadDir: t.TempDir(),
		MaxBytes:    1 << 20,
		Now:         func() time.Time { return now },
	})
}

func downloadHTTPResponse(req *http.Request, status int, headers map[string]string, body []byte) *http.Response {
	header := make(http.Header, len(headers))
	for key, value := range headers {
		header.Set(key, value)
	}
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        header,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func tinyDownloadPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 0x80, G: 0x40, B: 0x20, A: 0xff})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode returned error: %v", err)
	}
	return buf.Bytes()
}

func tinyDownloadJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.NRGBA{R: 0x80, G: 0x40, B: 0x20, A: 0xff})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("jpeg.Encode returned error: %v", err)
	}
	return buf.Bytes()
}

func tinyDownloadGIF(t *testing.T) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{
		color.NRGBA{R: 0x80, G: 0x40, B: 0x20, A: 0xff},
	})
	img.SetColorIndex(0, 0, 0)
	var buf bytes.Buffer
	if err := gif.Encode(&buf, img, nil); err != nil {
		t.Fatalf("gif.Encode returned error: %v", err)
	}
	return buf.Bytes()
}

func ipAddr(value string) net.IPAddr {
	return net.IPAddr{IP: net.ParseIP(value)}
}

func assertSafeDownloadPath(t *testing.T, path string) {
	t.Helper()
	if strings.TrimSpace(path) == "" {
		t.Fatal("download path is empty")
	}
	assertNoSecretText(t, path, "https://", "http://", "cdn.example", "assets.example", "access_token", "refresh_token", "client_secret", "Authorization", "Cookie", "oauth:")
}

func assertNoSecretText(t *testing.T, value string, notWant ...string) {
	t.Helper()
	for _, item := range notWant {
		if item == "" {
			continue
		}
		if strings.Contains(value, item) {
			t.Fatalf("%q unexpectedly contains %q", value, item)
		}
	}
}

func readDirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatalf("ReadDir(%q) returned error: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
