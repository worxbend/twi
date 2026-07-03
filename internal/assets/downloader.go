package assets

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/w0rxbend/twi/internal/debuglog"
	"github.com/w0rxbend/twi/internal/storage"
)

const (
	defaultAssetDownloadMaxBytes int64 = 8 << 20
	defaultAssetRedirectLimit          = 10
	defaultAssetDialTimeout            = 30 * time.Second
	defaultAssetDialKeepAlive          = 30 * time.Second
)

var (
	// ErrAssetDownloadFailed reports a transport or filesystem failure while
	// downloading image bytes. The error text never includes the source URL.
	ErrAssetDownloadFailed = errors.New("asset download failed")
	// ErrAssetDownloadInvalidURL reports a malformed or non-HTTP(S) source URL.
	ErrAssetDownloadInvalidURL = errors.New("invalid asset source URL")
	// ErrAssetDownloadUnsafeSource reports a URL that could reach local/private
	// resources or carry credential-shaped data.
	ErrAssetDownloadUnsafeSource = errors.New("unsafe asset source URL")
	// ErrAssetDownloadBadStatus reports a non-2xx HTTP response.
	ErrAssetDownloadBadStatus = errors.New("asset source returned unsuccessful status")
	// ErrAssetDownloadTooLarge reports a response body exceeding the byte limit.
	ErrAssetDownloadTooLarge = errors.New("asset download exceeds size limit")
	// ErrAssetDownloadUnsupportedMediaType reports downloaded bytes that are not
	// a supported PNG, JPEG, or GIF image source.
	ErrAssetDownloadUnsupportedMediaType = errors.New("unsupported asset download media type")
	// ErrAssetDownloadUnsafePath reports a configured download directory that
	// could leak URL or credential-shaped values into cache paths.
	ErrAssetDownloadUnsafePath = errors.New("unsafe asset download path")
)

// HostResolver resolves hostnames before the public image downloader makes an
// HTTP request. net.Resolver satisfies this interface.
type HostResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

// PublicImageDownloaderOptions configures the default HTTP downloader used for
// public metadata image URLs. Zero values use conservative defaults.
type PublicImageDownloaderOptions struct {
	// HTTPClient supplies timeout and redirect settings. Transport and Jar are
	// ignored so the downloader can enforce public host dialing and no cookies.
	HTTPClient *http.Client
	Resolver   HostResolver
	// DialContext is used by the default transport after host validation. Tests
	// can replace it without allowing the downloader to bypass public IP checks.
	DialContext func(context.Context, string, string) (net.Conn, error)
	transport   http.RoundTripper
	DownloadDir string
	MaxBytes    int64
	Now         func() time.Time
	Logger      debuglog.Logger
}

// PublicImageDownloader downloads public PNG, JPEG, and GIF image sources into
// URL-free local files for the asset cache and image preparer.
type PublicImageDownloader struct {
	Options PublicImageDownloaderOptions
}

var _ Downloader = (*PublicImageDownloader)(nil)

// NewPublicImageDownloader creates the default context-aware image downloader.
func NewPublicImageDownloader(options PublicImageDownloaderOptions) *PublicImageDownloader {
	return &PublicImageDownloader{Options: options}
}

// Download fetches a single public image source. Returned errors are safe for
// app-facing asset events and do not include source URLs, cookies, or headers.
func (d *PublicImageDownloader) Download(ctx context.Context, req DownloadRequest) (DownloadResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return DownloadResult{}, err
	}
	d.logDownload(ctx, "asset.download.start", req.URL, nil)

	source, err := d.validateSourceURL(ctx, req.URL)
	if err != nil {
		d.logDownload(ctx, "asset.download.failed", req.URL, err)
		return DownloadResult{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		err = fmt.Errorf("%w: create request", ErrAssetDownloadFailed)
		d.logDownload(ctx, "asset.download.failed", req.URL, err)
		return DownloadResult{}, err
	}

	resp, err := d.httpClient().Do(httpReq)
	if err != nil {
		err = safeAssetDownloadHTTPError(ctx, err)
		d.logDownload(ctx, "asset.download.failed", req.URL, err)
		return DownloadResult{}, err
	}
	if resp.Body == nil {
		resp.Body = io.NopCloser(bytes.NewReader(nil))
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("%w: HTTP %d", ErrAssetDownloadBadStatus, resp.StatusCode)
		d.logDownload(ctx, "asset.download.failed", req.URL, err, slog.Int("http_status", resp.StatusCode))
		return DownloadResult{}, err
	}

	opts := d.options()
	if resp.ContentLength > opts.MaxBytes {
		d.logDownload(ctx, "asset.download.failed", req.URL, ErrAssetDownloadTooLarge, slog.Int64("content_length", resp.ContentLength))
		return DownloadResult{}, ErrAssetDownloadTooLarge
	}
	data, err := readAssetDownloadBody(ctx, resp.Body, opts.MaxBytes)
	if err != nil {
		d.logDownload(ctx, "asset.download.failed", req.URL, err)
		return DownloadResult{}, err
	}
	mediaType, err := downloadedImageMediaType(resp.Header.Get("Content-Type"), req.MediaType, data)
	if err != nil {
		d.logDownload(ctx, "asset.download.failed", req.URL, err, slog.Int("byte_count", len(data)))
		return DownloadResult{}, err
	}
	path, err := d.writeDownload(ctx, data)
	if err != nil {
		d.logDownload(ctx, "asset.download.failed", req.URL, err, slog.Int("byte_count", len(data)))
		return DownloadResult{}, err
	}

	d.logDownload(ctx, "asset.download.succeeded", req.URL, nil,
		slog.Int("byte_count", len(data)),
		slog.String("media_type", mediaType),
	)
	return DownloadResult{
		Path:            path,
		PayloadIdentity: downloadPayloadIdentity(data),
		MediaType:       mediaType,
		FetchedAt:       opts.Now(),
		TemporaryPath:   true,
	}, nil
}

func (d *PublicImageDownloader) logDownload(ctx context.Context, event, rawURL string, err error, attrs ...slog.Attr) {
	opts := d.options()
	fields := debuglog.URLFields("source_url", rawURL)
	fields = append(fields,
		slog.Bool("has_error", err != nil),
	)
	if err != nil {
		fields = append(fields, slog.String("error", err.Error()))
	}
	fields = append(fields, attrs...)
	opts.Logger.Log(ctx, event, fields...)
}

func (d *PublicImageDownloader) options() PublicImageDownloaderOptions {
	var opts PublicImageDownloaderOptions
	if d != nil {
		opts = d.Options
	}
	if opts.MaxBytes <= 0 {
		opts.MaxBytes = defaultAssetDownloadMaxBytes
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return opts
}

func (d *PublicImageDownloader) httpClient() *http.Client {
	opts := d.options()
	var client http.Client
	if opts.HTTPClient != nil {
		client = *opts.HTTPClient
	}
	client.Jar = nil
	if opts.transport != nil {
		client.Transport = opts.transport
	} else {
		client.Transport = d.publicTransport()
	}
	previousCheck := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= defaultAssetRedirectLimit {
			return fmt.Errorf("%w: too many redirects", ErrAssetDownloadUnsafeSource)
		}
		if err := d.validateParsedSourceURL(req.Context(), req.URL); err != nil {
			return err
		}
		if previousCheck != nil {
			return previousCheck(req, via)
		}
		return nil
	}
	return &client
}

func (d *PublicImageDownloader) publicTransport() http.RoundTripper {
	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return http.DefaultTransport
	}
	transport := base.Clone()
	transport.Proxy = nil
	transport.DialContext = d.publicDialContext
	return transport
}

func (d *PublicImageDownloader) publicDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("%w: dial address is invalid", ErrAssetDownloadUnsafeSource)
	}
	if err := validateDownloadPort(port); err != nil {
		return nil, err
	}
	addrs, err := d.publicHostAddrs(ctx, host)
	if err != nil {
		return nil, err
	}

	dial := d.options().DialContext
	if dial == nil {
		dialer := &net.Dialer{
			Timeout:   defaultAssetDialTimeout,
			KeepAlive: defaultAssetDialKeepAlive,
		}
		dial = dialer.DialContext
	}
	for _, addr := range addrs {
		conn, err := dial(ctx, network, net.JoinHostPort(addr.String(), port))
		if err == nil {
			return conn, nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
	}
	return nil, fmt.Errorf("%w: connect to validated host", ErrAssetDownloadFailed)
}

func (d *PublicImageDownloader) validateSourceURL(ctx context.Context, raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("%w: missing source", ErrAssetDownloadInvalidURL)
	}
	if containsCredentialMarker(raw) {
		return nil, fmt.Errorf("%w: credential marker", ErrAssetDownloadUnsafeSource)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: malformed source", ErrAssetDownloadInvalidURL)
	}
	if err := d.validateParsedSourceURL(ctx, parsed); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (d *PublicImageDownloader) validateParsedSourceURL(ctx context.Context, parsed *url.URL) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if parsed == nil || !parsed.IsAbs() {
		return fmt.Errorf("%w: source must be absolute", ErrAssetDownloadInvalidURL)
	}
	scheme := strings.ToLower(strings.TrimSpace(parsed.Scheme))
	if scheme != "http" && scheme != "https" {
		return fmt.Errorf("%w: source must be HTTP or HTTPS", ErrAssetDownloadInvalidURL)
	}
	if parsed.User != nil {
		return fmt.Errorf("%w: source must not include credentials", ErrAssetDownloadUnsafeSource)
	}
	if parsed.Host == "" || parsed.Opaque != "" {
		return fmt.Errorf("%w: source host is invalid", ErrAssetDownloadInvalidURL)
	}
	if port := parsed.Port(); port != "" {
		if err := validateDownloadPort(port); err != nil {
			return err
		}
	}
	if parsed.Fragment != "" || sourceURLContainsCredentialMarker(parsed) {
		return fmt.Errorf("%w: source must not include credential-shaped values", ErrAssetDownloadUnsafeSource)
	}
	if err := d.validatePublicHost(ctx, parsed.Hostname()); err != nil {
		return err
	}
	return nil
}

func sourceURLContainsCredentialMarker(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	for _, value := range []string{parsed.RawQuery, parsed.EscapedPath()} {
		if containsCredentialMarker(value) {
			return true
		}
		if unescaped, err := url.QueryUnescape(value); err == nil && containsCredentialMarker(unescaped) {
			return true
		}
		if unescaped, err := url.PathUnescape(value); err == nil && containsCredentialMarker(unescaped) {
			return true
		}
	}
	return false
}

func validateDownloadPort(port string) error {
	n, err := strconv.Atoi(port)
	if err != nil || n <= 0 || n > 65535 {
		return fmt.Errorf("%w: source port is invalid", ErrAssetDownloadInvalidURL)
	}
	return nil
}

func (d *PublicImageDownloader) validatePublicHost(ctx context.Context, hostname string) error {
	_, err := d.publicHostAddrs(ctx, hostname)
	return err
}

func (d *PublicImageDownloader) publicHostAddrs(ctx context.Context, hostname string) ([]netip.Addr, error) {
	host := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(hostname)), ".")
	if host == "" || strings.ContainsAny(host, "\x00\r\n\t /\\") || strings.Contains(host, "%") {
		return nil, fmt.Errorf("%w: source host is unsafe", ErrAssetDownloadUnsafeSource)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return nil, fmt.Errorf("%w: source host is local", ErrAssetDownloadUnsafeSource)
	}

	if ip := net.ParseIP(host); ip != nil {
		addr, ok := publicDownloadAddr(ip)
		if !ok {
			return nil, fmt.Errorf("%w: source host is private", ErrAssetDownloadUnsafeSource)
		}
		return []netip.Addr{addr}, nil
	}

	resolver := d.options().Resolver
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	addrs, err := resolver.LookupIPAddr(ctx, host)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return nil, fmt.Errorf("%w: source host could not be validated", ErrAssetDownloadUnsafeSource)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("%w: source host could not be validated", ErrAssetDownloadUnsafeSource)
	}
	out := make([]netip.Addr, 0, len(addrs))
	for _, addr := range addrs {
		ip, ok := publicDownloadAddr(addr.IP)
		if !ok {
			return nil, fmt.Errorf("%w: source host is private", ErrAssetDownloadUnsafeSource)
		}
		out = append(out, ip)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func publicDownloadAddr(ip net.IP) (netip.Addr, bool) {
	if ip == nil {
		return netip.Addr{}, false
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		return netip.Addr{}, false
	}
	addr = addr.Unmap()
	if !addr.IsValid() ||
		!addr.IsGlobalUnicast() ||
		addr.IsPrivate() ||
		addr.IsLoopback() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsMulticast() ||
		addr.IsUnspecified() {
		return netip.Addr{}, false
	}
	for _, prefix := range unsafeDownloadIPPrefixes {
		if prefix.Contains(addr) {
			return netip.Addr{}, false
		}
	}
	return addr, true
}

var unsafeDownloadIPPrefixes = []netip.Prefix{
	mustParseDownloadPrefix("0.0.0.0/8"),
	mustParseDownloadPrefix("10.0.0.0/8"),
	mustParseDownloadPrefix("100.64.0.0/10"),
	mustParseDownloadPrefix("127.0.0.0/8"),
	mustParseDownloadPrefix("169.254.0.0/16"),
	mustParseDownloadPrefix("172.16.0.0/12"),
	mustParseDownloadPrefix("192.0.0.0/24"),
	mustParseDownloadPrefix("192.0.2.0/24"),
	mustParseDownloadPrefix("192.88.99.0/24"),
	mustParseDownloadPrefix("192.168.0.0/16"),
	mustParseDownloadPrefix("198.18.0.0/15"),
	mustParseDownloadPrefix("198.51.100.0/24"),
	mustParseDownloadPrefix("203.0.113.0/24"),
	mustParseDownloadPrefix("224.0.0.0/4"),
	mustParseDownloadPrefix("240.0.0.0/4"),
	mustParseDownloadPrefix("255.255.255.255/32"),
	mustParseDownloadPrefix("::/128"),
	mustParseDownloadPrefix("::1/128"),
	mustParseDownloadPrefix("64:ff9b::/96"),
	mustParseDownloadPrefix("64:ff9b:1::/48"),
	mustParseDownloadPrefix("100::/64"),
	mustParseDownloadPrefix("2001::/23"),
	mustParseDownloadPrefix("2001:2::/48"),
	mustParseDownloadPrefix("2001:db8::/32"),
	mustParseDownloadPrefix("2002::/16"),
	mustParseDownloadPrefix("fc00::/7"),
	mustParseDownloadPrefix("fe80::/10"),
	mustParseDownloadPrefix("ff00::/8"),
}

func mustParseDownloadPrefix(value string) netip.Prefix {
	prefix, err := netip.ParsePrefix(value)
	if err != nil {
		panic(err)
	}
	return prefix
}

func safeAssetDownloadHTTPError(ctx context.Context, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		return context.DeadlineExceeded
	case errors.Is(err, ErrAssetDownloadInvalidURL):
		return fmt.Errorf("%w: redirect rejected", ErrAssetDownloadInvalidURL)
	case errors.Is(err, ErrAssetDownloadUnsafeSource):
		return fmt.Errorf("%w: redirect rejected", ErrAssetDownloadUnsafeSource)
	default:
		return fmt.Errorf("%w: request failed", ErrAssetDownloadFailed)
	}
}

func readAssetDownloadBody(ctx context.Context, body io.Reader, maxBytes int64) ([]byte, error) {
	if body == nil {
		body = bytes.NewReader(nil)
	}
	limited := io.LimitReader(body, maxBytes+1)
	var out bytes.Buffer
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := limited.Read(buf)
		if n > 0 {
			out.Write(buf[:n])
			if int64(out.Len()) > maxBytes {
				return nil, ErrAssetDownloadTooLarge
			}
		}
		if errors.Is(readErr, io.EOF) {
			return out.Bytes(), nil
		}
		if readErr != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			return nil, fmt.Errorf("%w: read response", ErrAssetDownloadFailed)
		}
	}
}

func downloadedImageMediaType(headerValue, metadataValue string, data []byte) (string, error) {
	sniffed := canonicalMediaType(http.DetectContentType(sniffBytes(data)))
	if mediaType := canonicalSupportedImageMediaType(sniffed); mediaType != "" {
		return mediaType, nil
	}
	return "", ErrAssetDownloadUnsupportedMediaType
}

func downloadPayloadIdentity(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sniffBytes(data []byte) []byte {
	if len(data) > 512 {
		return data[:512]
	}
	return data
}

func canonicalSupportedImageMediaType(value string) string {
	mediaType := canonicalMediaType(value)
	switch mediaType {
	case "image/png", "application/png":
		return "image/png"
	case "image/jpeg", "image/jpg", "image/pjpeg":
		return "image/jpeg"
	case "image/gif":
		return "image/gif"
	default:
		return ""
	}
}

func canonicalMediaType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	mediaType, _, err := mime.ParseMediaType(value)
	if err != nil {
		if before, _, ok := strings.Cut(value, ";"); ok {
			mediaType = before
		} else {
			mediaType = value
		}
	}
	return strings.ToLower(strings.TrimSpace(mediaType))
}

func (d *PublicImageDownloader) writeDownload(ctx context.Context, data []byte) (string, error) {
	dir, err := d.downloadDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: create download directory", ErrAssetDownloadFailed)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(dir, "asset-*.bin")
	if err != nil {
		return "", fmt.Errorf("%w: create download file", ErrAssetDownloadFailed)
	}
	path := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := writeAssetDownloadFile(ctx, file, data); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("%w: write download file", ErrAssetDownloadFailed)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	remove = false
	return path, nil
}

func (d *PublicImageDownloader) downloadDir() (string, error) {
	dir := ""
	if d != nil {
		dir = strings.TrimSpace(d.Options.DownloadDir)
	}
	if dir == "" {
		root, err := storage.DefaultAssetCacheDir()
		if err != nil {
			return "", fmt.Errorf("%w: default cache directory unavailable", ErrAssetDownloadFailed)
		}
		dir = filepath.Join(root, "downloads")
	}
	if strings.Contains(strings.ToLower(dir), "://") || containsCredentialMarker(dir) {
		return "", ErrAssetDownloadUnsafePath
	}
	return dir, nil
}

func writeAssetDownloadFile(ctx context.Context, file *os.File, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Write(data)
		if err != nil {
			return fmt.Errorf("%w: write download file", ErrAssetDownloadFailed)
		}
		if n == 0 {
			return fmt.Errorf("%w: write download file", ErrAssetDownloadFailed)
		}
		data = data[n:]
	}
	return ctx.Err()
}
