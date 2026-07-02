package render

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/w0rxbend/twi/internal/storage"
)

const kittyPNGFormat = 100

const (
	defaultMaxImageSourceBytes  int64 = 8 << 20
	defaultMaxImageSourcePixels       = 16 * 1024 * 1024
	defaultMaxPreparedPixels          = 1024 * 1024
	defaultCellPixelWidth             = 16
	defaultCellPixelHeight            = 32
)

var (
	// ErrImageUnsupported reports that the terminal or config state should use
	// the already-reserved text fallback instead of inline image output.
	ErrImageUnsupported = errors.New("image renderer unsupported")
	// ErrImageRenderFailed reports that an otherwise supported renderer could
	// not produce terminal image output for a cached asset.
	ErrImageRenderFailed = errors.New("image render failed")
	// ErrImagePreparationFailed reports that a downloaded image could not be
	// decoded or normalized into a renderer-ready PNG record.
	ErrImagePreparationFailed = errors.New("image preparation failed")
	// ErrImageUnsupportedMediaType reports a media type that the preparation
	// step intentionally does not attempt to decode.
	ErrImageUnsupportedMediaType = errors.New("unsupported image media type")
	// ErrImageUnsafeAsset reports an image identity or path that may contain a
	// URL, filesystem detail, or credential-shaped value.
	ErrImageUnsafeAsset = errors.New("unsafe image asset")
	// ErrImageCorruptData reports image bytes that cannot be decoded as the
	// advertised or sniffed image format.
	ErrImageCorruptData = errors.New("corrupt image data")
	// ErrImageTooLarge reports image bytes, decoded dimensions, or prepared
	// output dimensions that exceed configured bounds.
	ErrImageTooLarge = errors.New("image exceeds configured bounds")
)

// IsPermanentImageFailure reports errors that should keep the stable text
// fallback for the same downloaded record and requested cell dimensions.
func IsPermanentImageFailure(err error) bool {
	return errors.Is(err, ErrImageUnsupportedMediaType) ||
		errors.Is(err, ErrImageUnsafeAsset) ||
		errors.Is(err, ErrImageCorruptData) ||
		errors.Is(err, ErrImageTooLarge)
}

// ImagePrepareOptions bounds decode and transform work for untrusted image
// bytes. Zero values use conservative defaults suitable for chat assets.
type ImagePrepareOptions struct {
	MaxSourceBytes  int64
	MaxSourcePixels int
	MaxOutputPixels int
	CellPixelWidth  int
	CellPixelHeight int
	PreparedDir     string
}

// PNGImagePreparer decodes PNG, JPEG, and GIF first-frame assets and writes a
// renderer-ready PNG sized to the requested terminal cell rectangle.
type PNGImagePreparer struct {
	Options ImagePrepareOptions
}

var _ ImagePreparer = (*PNGImagePreparer)(nil)

// NewPNGImagePreparer creates a bounded standard-library image preparer.
func NewPNGImagePreparer(options ImagePrepareOptions) *PNGImagePreparer {
	return &PNGImagePreparer{Options: options}
}

// PreparedImageRenderer composes a preparer and terminal renderer behind the
// existing ImageRenderer boundary.
type PreparedImageRenderer struct {
	Preparer ImagePreparer
	Renderer ImageRenderer
}

var _ ImageRenderer = (*PreparedImageRenderer)(nil)

// RenderImage normalizes asset bytes before delegating to the terminal
// renderer. Every failure returns a fixed-width fallback cell.
func (r *PreparedImageRenderer) RenderImage(ctx context.Context, asset storage.AssetRecord, spec ImageSpec) (ImageCell, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cell := fallbackImageCell(asset, spec)
	if err := ctx.Err(); err != nil {
		return cell, err
	}
	if r == nil || r.Renderer == nil {
		return cell, fmt.Errorf("%w: missing image renderer", ErrImageRenderFailed)
	}
	record := asset
	if r.Preparer != nil {
		prepared, err := r.Preparer.PrepareImage(ctx, asset, spec)
		if err != nil {
			return cell, err
		}
		record = prepared
	}
	rendered, err := r.Renderer.RenderImage(ctx, record, spec)
	if err != nil {
		return cell, err
	}
	return rendered, nil
}

// PrepareImage decodes and normalizes one downloaded asset into PNG. It never
// includes source paths, source URLs, or asset identifiers in returned errors.
func (p *PNGImagePreparer) PrepareImage(ctx context.Context, asset storage.AssetRecord, spec ImageSpec) (storage.AssetRecord, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	opts := p.options()
	record := preparedRecordSkeleton(asset, spec)
	if err := ctx.Err(); err != nil {
		return record, err
	}
	if err := validatePreparationRecord(asset); err != nil {
		return record, err
	}
	if !supportedPreparationMediaType(asset.MediaType) {
		return record, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsupportedMediaType)
	}

	data, err := readImageFileBounded(ctx, asset.Path, opts.MaxSourceBytes)
	if err != nil {
		return record, err
	}
	config, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return record, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageCorruptData)
	}
	if !supportedDecodedImageFormat(format) {
		return record, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsupportedMediaType)
	}
	if err := validateDecodedImageBounds(config, opts.MaxSourcePixels); err != nil {
		return record, err
	}
	if err := ctx.Err(); err != nil {
		return record, err
	}

	source, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return record, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageCorruptData)
	}
	if !supportedDecodedImageFormat(format) {
		return record, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsupportedMediaType)
	}
	targetWidth, targetHeight, err := preparedPixelSize(record.WidthCells, record.HeightCells, opts)
	if err != nil {
		return record, err
	}
	prepared := cropScaleImage(source, targetWidth, targetHeight)
	if err := ctx.Err(); err != nil {
		return record, err
	}
	path, err := writePreparedPNG(ctx, opts, asset, data, record.WidthCells, record.HeightCells, prepared)
	if err != nil {
		return record, err
	}
	record.Path = path
	return record, nil
}

func (p *PNGImagePreparer) options() ImagePrepareOptions {
	if p != nil {
		return p.Options.normalized()
	}
	return ImagePrepareOptions{}.normalized()
}

func (o ImagePrepareOptions) normalized() ImagePrepareOptions {
	if o.MaxSourceBytes <= 0 {
		o.MaxSourceBytes = defaultMaxImageSourceBytes
	}
	if o.MaxSourcePixels <= 0 {
		o.MaxSourcePixels = defaultMaxImageSourcePixels
	}
	if o.MaxOutputPixels <= 0 {
		o.MaxOutputPixels = defaultMaxPreparedPixels
	}
	if o.CellPixelWidth <= 0 {
		o.CellPixelWidth = defaultCellPixelWidth
	}
	if o.CellPixelHeight <= 0 {
		o.CellPixelHeight = defaultCellPixelHeight
	}
	return o
}

func preparedRecordSkeleton(asset storage.AssetRecord, spec ImageSpec) storage.AssetRecord {
	record := asset
	record.Path = ""
	record.SourceURL = ""
	record.MediaType = "image/png"
	record.WidthCells = positiveFirst(spec.WidthCells, asset.WidthCells, textWidth(spec.Fallback), 1)
	record.HeightCells = positiveFirst(spec.HeightCells, asset.HeightCells, 1)
	return record
}

func validatePreparationRecord(asset storage.AssetRecord) error {
	if containsUnsafeImageIdentity(asset.Key.Kind) || containsUnsafeImageIdentity(asset.Key.ID) {
		return fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsafeAsset)
	}
	path := strings.TrimSpace(asset.Path)
	if path == "" {
		return fmt.Errorf("%w: missing source image file", ErrImagePreparationFailed)
	}
	if containsUnsafeImageIdentity(path) {
		return fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsafeAsset)
	}
	return nil
}

func supportedPreparationMediaType(mediaType string) bool {
	mediaType = normalizeMediaType(mediaType)
	switch mediaType {
	case "", "application/octet-stream", "image/png", "application/png", "image/jpeg", "image/jpg", "image/pjpeg", "image/gif":
		return true
	default:
		return false
	}
}

func supportedDecodedImageFormat(format string) bool {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png", "jpeg", "gif":
		return true
	default:
		return false
	}
}

func normalizeMediaType(mediaType string) string {
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	if semicolon := strings.IndexByte(mediaType, ';'); semicolon >= 0 {
		mediaType = strings.TrimSpace(mediaType[:semicolon])
	}
	return mediaType
}

func readImageFileBounded(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("%w: source image is unavailable", ErrImagePreparationFailed)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: source image is unavailable", ErrImagePreparationFailed)
	}
	if info.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge)
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("%w: source image is unreadable", ErrImagePreparationFailed)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: read source image", ErrImagePreparationFailed)
	}
	if int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return data, nil
}

func validateDecodedImageBounds(config image.Config, maxPixels int) error {
	if config.Width <= 0 || config.Height <= 0 {
		return fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageCorruptData)
	}
	pixels := int64(config.Width) * int64(config.Height)
	if pixels <= 0 || pixels > int64(maxPixels) {
		return fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge)
	}
	return nil
}

func preparedPixelSize(widthCells, heightCells int, opts ImagePrepareOptions) (int, int, error) {
	width := widthCells * opts.CellPixelWidth
	height := heightCells * opts.CellPixelHeight
	if width <= 0 || height <= 0 {
		return 0, 0, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge)
	}
	pixels := int64(width) * int64(height)
	if pixels <= 0 || pixels > int64(opts.MaxOutputPixels) {
		return 0, 0, fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge)
	}
	return width, height, nil
}

func cropScaleImage(source image.Image, targetWidth, targetHeight int) *image.NRGBA {
	sourceBounds := source.Bounds()
	crop := centerCropRect(sourceBounds, targetWidth, targetHeight)
	out := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	cropWidth := crop.Dx()
	cropHeight := crop.Dy()
	for y := 0; y < targetHeight; y++ {
		sy := crop.Min.Y + y*cropHeight/targetHeight
		if sy >= crop.Max.Y {
			sy = crop.Max.Y - 1
		}
		for x := 0; x < targetWidth; x++ {
			sx := crop.Min.X + x*cropWidth/targetWidth
			if sx >= crop.Max.X {
				sx = crop.Max.X - 1
			}
			out.Set(x, y, source.At(sx, sy))
		}
	}
	return out
}

func centerCropRect(bounds image.Rectangle, targetWidth, targetHeight int) image.Rectangle {
	if bounds.Empty() || targetWidth <= 0 || targetHeight <= 0 {
		return bounds
	}
	sourceWidth := bounds.Dx()
	sourceHeight := bounds.Dy()
	if int64(sourceWidth)*int64(targetHeight) > int64(sourceHeight)*int64(targetWidth) {
		cropWidth := int(int64(sourceHeight) * int64(targetWidth) / int64(targetHeight))
		if cropWidth < 1 {
			cropWidth = 1
		}
		x0 := bounds.Min.X + (sourceWidth-cropWidth)/2
		return image.Rect(x0, bounds.Min.Y, x0+cropWidth, bounds.Max.Y)
	}
	cropHeight := int(int64(sourceWidth) * int64(targetHeight) / int64(targetWidth))
	if cropHeight < 1 {
		cropHeight = 1
	}
	y0 := bounds.Min.Y + (sourceHeight-cropHeight)/2
	return image.Rect(bounds.Min.X, y0, bounds.Max.X, y0+cropHeight)
}

func writePreparedPNG(ctx context.Context, opts ImagePrepareOptions, asset storage.AssetRecord, data []byte, widthCells, heightCells int, img image.Image) (string, error) {
	dir, err := preparedImageDir(opts, asset)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("%w: prepared image directory is unavailable", ErrImagePreparationFailed)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := filepath.Join(dir, preparedImageFilename(asset, data, widthCells, heightCells))
	tmp, err := os.CreateTemp(dir, ".prepared-*.tmp")
	if err != nil {
		return "", fmt.Errorf("%w: prepared image file is unavailable", ErrImagePreparationFailed)
	}
	tmpPath := tmp.Name()
	removeTmp := true
	defer func() {
		if removeTmp {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := png.Encode(tmp, img); err != nil {
		_ = tmp.Close()
		return "", fmt.Errorf("%w: encode prepared image", ErrImagePreparationFailed)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("%w: write prepared image", ErrImagePreparationFailed)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(path)
		if err := os.Rename(tmpPath, path); err != nil {
			return "", fmt.Errorf("%w: publish prepared image", ErrImagePreparationFailed)
		}
	}
	removeTmp = false
	return path, nil
}

func preparedImageDir(opts ImagePrepareOptions, asset storage.AssetRecord) (string, error) {
	if dir := strings.TrimSpace(opts.PreparedDir); dir != "" {
		if containsUnsafeImageIdentity(dir) {
			return "", fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsafeAsset)
		}
		return dir, nil
	}
	dir := filepath.Dir(asset.Path)
	if dir == "." || strings.TrimSpace(dir) == "" || containsUnsafeImageIdentity(dir) {
		return "", fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsafeAsset)
	}
	return dir, nil
}

func preparedImageFilename(asset storage.AssetRecord, data []byte, widthCells, heightCells int) string {
	sourceDigest := sha256.Sum256(data)
	input := strings.Join([]string{
		asset.Key.Kind,
		asset.Key.ID,
		strconv.Itoa(widthCells),
		strconv.Itoa(heightCells),
		hex.EncodeToString(sourceDigest[:]),
	}, "\x00")
	sum := sha256.Sum256([]byte(input))
	return "prepared-" + hex.EncodeToString(sum[:16]) + ".png"
}

// KittyRenderer renders prepared local PNG assets with the Kitty graphics
// protocol. It is intended for asynchronous callers; View paths should render
// stable fallback fragments until a cell has been prepared.
type KittyRenderer struct {
	Decision ImageCapabilityDecision
}

var _ ImageRenderer = (*KittyRenderer)(nil)

// NewKittyRenderer creates a Kitty-compatible renderer from the resolved image
// capability state shared by app startup and diagnostics.
func NewKittyRenderer(decision ImageCapabilityDecision) *KittyRenderer {
	return &KittyRenderer{Decision: decision}
}

// RenderImage returns terminal output for one cached image while preserving the
// requested layout width on every error path.
func (r *KittyRenderer) RenderImage(ctx context.Context, asset storage.AssetRecord, spec ImageSpec) (ImageCell, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cell := fallbackImageCell(asset, spec)
	if err := ctx.Err(); err != nil {
		return cell, err
	}
	if r == nil || !r.supported() {
		return cell, ErrImageUnsupported
	}
	if containsUnsafeImageIdentity(asset.Key.Kind) || containsUnsafeImageIdentity(asset.Key.ID) {
		return cell, fmt.Errorf("%w: %w", ErrImageRenderFailed, ErrImageUnsafeAsset)
	}

	format, ok := kittyImageFormat(asset)
	if !ok {
		return cell, fmt.Errorf("%w: %w", ErrImageRenderFailed, ErrImageUnsupportedMediaType)
	}
	path := strings.TrimSpace(asset.Path)
	if path == "" {
		return cell, fmt.Errorf("%w: missing cached image file", ErrImageRenderFailed)
	}
	if containsUnsafeImageIdentity(path) {
		return cell, fmt.Errorf("%w: %w", ErrImageRenderFailed, ErrImageUnsafeAsset)
	}
	info, err := os.Stat(path)
	if err != nil {
		return cell, fmt.Errorf("%w: cached image file is unavailable", ErrImageRenderFailed)
	}
	if info.IsDir() {
		return cell, fmt.Errorf("%w: cached image path is a directory", ErrImageRenderFailed)
	}
	file, err := os.Open(path)
	if err != nil {
		return cell, fmt.Errorf("%w: cached image file is unreadable", ErrImageRenderFailed)
	}
	if err := file.Close(); err != nil {
		return cell, fmt.Errorf("%w: cached image file close failed", ErrImageRenderFailed)
	}
	if err := ctx.Err(); err != nil {
		return cell, err
	}

	width := cell.WidthCells
	height := positiveFirst(spec.HeightCells, asset.HeightCells, 1)
	encodedPath := base64.StdEncoding.EncodeToString([]byte(path))
	escape := fmt.Sprintf(
		"\x1b_Ga=T,f=%d,t=f,q=2,C=1,i=%d,c=%d,r=%d;%s\x1b\\",
		format,
		kittyImageID(asset),
		width,
		height,
		encodedPath,
	)
	cell.Text = escape + strings.Repeat(" ", width)
	return cell, nil
}

func (r *KittyRenderer) supported() bool {
	decision := r.Decision
	if !decision.EnableKitty {
		return false
	}
	if !decision.Signals.KittyCompatible {
		return false
	}
	switch decision.Status {
	case ImageCapabilityEnabled, ImageCapabilityDegraded:
		return true
	default:
		return false
	}
}

func fallbackImageCell(asset storage.AssetRecord, spec ImageSpec) ImageCell {
	width := positiveFirst(spec.WidthCells, asset.WidthCells, textWidth(spec.Fallback), 1)
	return ImageCell{
		Text:       fitCells(spec.Fallback, width),
		WidthCells: width,
	}
}

func kittyImageFormat(asset storage.AssetRecord) (int, bool) {
	mediaType := normalizeMediaType(asset.MediaType)
	switch mediaType {
	case "", "image/png", "application/png":
		if mediaType == "" && strings.ToLower(filepath.Ext(asset.Path)) != ".png" {
			return 0, false
		}
		return kittyPNGFormat, true
	default:
		return 0, false
	}
}

func kittyImageID(asset storage.AssetRecord) uint32 {
	input := asset.Key.Kind + "\x00" + asset.Key.ID + "\x00" + asset.Path
	sum := sha256.Sum256([]byte(input))
	id := binary.BigEndian.Uint32(sum[:4])
	if id == 0 {
		return 1
	}
	return id
}

func positiveFirst(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
