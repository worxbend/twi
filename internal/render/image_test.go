package render

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/w0rxbend/twi/internal/storage"
	"github.com/w0rxbend/twi/internal/twitch"
)

func TestKittyRendererProducesFixedCellOutput(t *testing.T) {
	path := writeTinyPNG(t)
	asset := storage.AssetRecord{
		Key:         storage.AssetKey{Kind: "emoji", ID: "1f600"},
		Path:        path,
		MediaType:   "image/png",
		WidthCells:  2,
		HeightCells: 1,
	}
	spec := ImageSpec{
		Ref:         twitch.AssetRef{Kind: "emoji", ID: "1f600"},
		WidthCells:  4,
		HeightCells: 1,
		Fallback:    "😀",
	}
	renderer := NewKittyRenderer(supportedKittyDecision())

	cell, err := renderer.RenderImage(context.Background(), asset, spec)
	if err != nil {
		t.Fatalf("RenderImage returned error: %v", err)
	}
	if cell.WidthCells != 4 {
		t.Fatalf("cell.WidthCells = %d, want 4", cell.WidthCells)
	}
	if !strings.HasPrefix(cell.Text, "\x1b_G") || !strings.Contains(cell.Text, "a=T") {
		t.Fatalf("cell.Text missing Kitty graphics command: %q", cell.Text)
	}
	for _, want := range []string{"f=100", "t=f", "q=2", "C=1", "c=4", "r=1"} {
		if !strings.Contains(cell.Text, want) {
			t.Fatalf("cell.Text = %q, want it to contain %q", cell.Text, want)
		}
	}
	if !strings.Contains(cell.Text, base64.StdEncoding.EncodeToString([]byte(path))) {
		t.Fatalf("cell.Text does not include encoded cached path: %q", cell.Text)
	}
	if !strings.HasSuffix(cell.Text, strings.Repeat(" ", 4)) {
		t.Fatalf("cell.Text should end with four width-reserving spaces: %q", cell.Text)
	}
}

func TestKittyRendererUnsupportedTerminalReturnsFallbackCell(t *testing.T) {
	asset := storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:      "does-not-need-to-exist.png",
		MediaType: "image/png",
	}
	spec := ImageSpec{WidthCells: 6, HeightCells: 1, Fallback: "Kappa"}
	renderer := NewKittyRenderer(ImageCapabilityDecision{
		Status:      ImageCapabilityUnsupported,
		EnableKitty: true,
		Signals:     TerminalImageSignals{KittyCompatible: false},
	})

	cell, err := renderer.RenderImage(context.Background(), asset, spec)
	if !errors.Is(err, ErrImageUnsupported) {
		t.Fatalf("RenderImage error = %v, want ErrImageUnsupported", err)
	}
	if cell.WidthCells != 6 {
		t.Fatalf("cell.WidthCells = %d, want 6", cell.WidthCells)
	}
	if got, want := cell.Text, "Kappa "; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
}

func TestKittyRendererFailurePreservesReservedWidth(t *testing.T) {
	secretLookingPath := filepath.Join(t.TempDir(), "oauth:fixture-token.png")
	spec := ImageSpec{WidthCells: 5, HeightCells: 1, Fallback: "[AL]"}
	renderer := NewKittyRenderer(supportedKittyDecision())

	cell, err := renderer.RenderImage(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "avatar", ID: "user-1"},
		Path:      secretLookingPath,
		MediaType: "image/png",
	}, spec)
	if !errors.Is(err, ErrImageRenderFailed) {
		t.Fatalf("RenderImage error = %v, want ErrImageRenderFailed", err)
	}
	if strings.Contains(err.Error(), "oauth:fixture-token") || strings.Contains(err.Error(), secretLookingPath) {
		t.Fatalf("RenderImage error leaked cached path detail: %v", err)
	}
	if cell.WidthCells != 5 {
		t.Fatalf("cell.WidthCells = %d, want 5", cell.WidthCells)
	}
	if got, want := cell.Text, "[AL] "; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
}

func TestKittyRendererCancellationReturnsFallbackCell(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := ImageSpec{WidthCells: 2, HeightCells: 1, Fallback: "😀"}
	renderer := NewKittyRenderer(supportedKittyDecision())

	cell, err := renderer.RenderImage(ctx, storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "emoji", ID: "1f600"},
		Path:      writeTinyPNG(t),
		MediaType: "image/png",
	}, spec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("RenderImage error = %v, want context.Canceled", err)
	}
	if cell.WidthCells != 2 {
		t.Fatalf("cell.WidthCells = %d, want 2", cell.WidthCells)
	}
	if got, want := cell.Text, "😀"; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
}

func TestKittyRendererRejectsUnsupportedMediaTypeWithFallback(t *testing.T) {
	spec := ImageSpec{WidthCells: 6, HeightCells: 1, Fallback: "Kappa"}
	renderer := NewKittyRenderer(supportedKittyDecision())

	cell, err := renderer.RenderImage(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:      writeTinyPNG(t),
		MediaType: "image/webp",
	}, spec)
	if !errors.Is(err, ErrImageRenderFailed) {
		t.Fatalf("RenderImage error = %v, want ErrImageRenderFailed", err)
	}
	if !IsPermanentImageFailure(err) {
		t.Fatalf("RenderImage error = %v, want permanent image failure", err)
	}
	if got, want := cell.Text, "Kappa "; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
	if cell.WidthCells != 6 {
		t.Fatalf("cell.WidthCells = %d, want 6", cell.WidthCells)
	}
}

func TestImageFailurePermanentClassification(t *testing.T) {
	permanent := []error{
		fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsupportedMediaType),
		fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageUnsafeAsset),
		fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageCorruptData),
		fmt.Errorf("%w: %w", ErrImagePreparationFailed, ErrImageTooLarge),
		fmt.Errorf("%w: %w", ErrImageRenderFailed, ErrImageUnsupportedMediaType),
	}
	for _, err := range permanent {
		if !IsPermanentImageFailure(err) {
			t.Fatalf("IsPermanentImageFailure(%v) = false, want true", err)
		}
	}

	transient := []error{
		context.Canceled,
		context.DeadlineExceeded,
		ErrImagePreparationFailed,
		ErrImageRenderFailed,
		ErrImageUnsupported,
		fmt.Errorf("%w: source image is unavailable", ErrImagePreparationFailed),
	}
	for _, err := range transient {
		if IsPermanentImageFailure(err) {
			t.Fatalf("IsPermanentImageFailure(%v) = true, want false", err)
		}
	}
}

func TestPNGImagePreparerNormalizesSupportedFormatsToFixedPNGRecords(t *testing.T) {
	fixtures := []struct {
		name      string
		mediaType string
		write     func(*testing.T) string
	}{
		{name: "png", mediaType: "image/png", write: writePNGImageFixture},
		{name: "jpeg", mediaType: "image/jpeg", write: writeJPEGImageFixture},
		{name: "gif", mediaType: "image/gif", write: writeGIFImageFixture},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			sourcePath := fixture.write(t)
			preparer := NewPNGImagePreparer(ImagePrepareOptions{
				CellPixelWidth:  10,
				CellPixelHeight: 12,
				PreparedDir:     t.TempDir(),
			})
			record, err := preparer.PrepareImage(context.Background(), storage.AssetRecord{
				Key:         storage.AssetKey{Kind: "twitch_emote", ID: "25"},
				Path:        sourcePath,
				SourceURL:   "https://cdn.example/emotes/25",
				MediaType:   fixture.mediaType,
				WidthCells:  1,
				HeightCells: 1,
			}, ImageSpec{
				Ref:         twitch.AssetRef{Kind: "twitch_emote", ID: "25"},
				WidthCells:  3,
				HeightCells: 2,
				Fallback:    "Kappa",
			})
			if err != nil {
				t.Fatalf("PrepareImage returned error: %v", err)
			}
			if record.MediaType != "image/png" {
				t.Fatalf("record.MediaType = %q, want image/png", record.MediaType)
			}
			if record.WidthCells != 3 || record.HeightCells != 2 {
				t.Fatalf("record cells = %dx%d, want 3x2", record.WidthCells, record.HeightCells)
			}
			if record.SourceURL != "" {
				t.Fatalf("prepared SourceURL = %q, want empty render-only record", record.SourceURL)
			}
			file, err := os.Open(record.Path)
			if err != nil {
				t.Fatalf("open prepared image: %v", err)
			}
			defer file.Close()
			config, format, err := image.DecodeConfig(file)
			if err != nil {
				t.Fatalf("DecodeConfig prepared image returned error: %v", err)
			}
			if format != "png" {
				t.Fatalf("prepared format = %q, want png", format)
			}
			if config.Width != 30 || config.Height != 24 {
				t.Fatalf("prepared pixels = %dx%d, want 30x24", config.Width, config.Height)
			}
			for _, unsafe := range []string{"https://", "access_token", "client_secret", "oauth:"} {
				if strings.Contains(record.Path, unsafe) {
					t.Fatalf("prepared path %q contains unsafe text %q", record.Path, unsafe)
				}
			}
		})
	}
}

func TestPNGImagePreparerRejectsUnsupportedAndCorruptMediaSafely(t *testing.T) {
	tests := []struct {
		name      string
		path      func(*testing.T) string
		mediaType string
		want      error
	}{
		{
			name:      "unsupported media type",
			path:      writePNGImageFixture,
			mediaType: "image/webp",
			want:      ErrImageUnsupportedMediaType,
		},
		{
			name: "corrupt image data",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "corrupt.png")
				if err := os.WriteFile(path, []byte("not an image"), 0o600); err != nil {
					t.Fatalf("write corrupt image: %v", err)
				}
				return path
			},
			mediaType: "image/png",
			want:      ErrImagePreparationFailed,
		},
		{
			name: "unsafe source path",
			path: func(t *testing.T) string {
				path := filepath.Join(t.TempDir(), "oauth:fixture-token.png")
				data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
				if err != nil {
					t.Fatalf("decode fixture PNG: %v", err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatalf("write unsafe-path image: %v", err)
				}
				return path
			},
			mediaType: "image/png",
			want:      ErrImagePreparationFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.path(t)
			preparer := NewPNGImagePreparer(ImagePrepareOptions{PreparedDir: t.TempDir()})
			_, err := preparer.PrepareImage(context.Background(), storage.AssetRecord{
				Key:       storage.AssetKey{Kind: "avatar", ID: "user-1"},
				Path:      path,
				MediaType: tt.mediaType,
			}, ImageSpec{WidthCells: 5, HeightCells: 1, Fallback: "[AL]"})
			if !errors.Is(err, tt.want) {
				t.Fatalf("PrepareImage error = %v, want %v", err, tt.want)
			}
			for _, unsafe := range []string{path, "oauth:fixture-token", "access_token", "client_secret"} {
				if unsafe != "" && strings.Contains(err.Error(), unsafe) {
					t.Fatalf("PrepareImage error leaked unsafe text %q in %q", unsafe, err.Error())
				}
			}
		})
	}
}

func TestPNGImagePreparerEnforcesSourceAndOutputBounds(t *testing.T) {
	t.Run("source byte limit", func(t *testing.T) {
		path := writePNGImageFixture(t)
		preparer := NewPNGImagePreparer(ImagePrepareOptions{
			MaxSourceBytes: 1,
			PreparedDir:    t.TempDir(),
		})
		_, err := preparer.PrepareImage(context.Background(), storage.AssetRecord{
			Key:       storage.AssetKey{Kind: "emoji", ID: "1f600"},
			Path:      path,
			MediaType: "image/png",
		}, ImageSpec{WidthCells: 2, HeightCells: 1, Fallback: "😀"})
		if !errors.Is(err, ErrImagePreparationFailed) {
			t.Fatalf("PrepareImage error = %v, want ErrImagePreparationFailed", err)
		}
		if strings.Contains(err.Error(), path) {
			t.Fatalf("PrepareImage error leaked source path: %v", err)
		}
	})

	t.Run("prepared output pixel limit", func(t *testing.T) {
		path := writePNGImageFixture(t)
		preparer := NewPNGImagePreparer(ImagePrepareOptions{
			MaxOutputPixels: 8,
			PreparedDir:     t.TempDir(),
		})
		_, err := preparer.PrepareImage(context.Background(), storage.AssetRecord{
			Key:       storage.AssetKey{Kind: "emoji", ID: "1f600"},
			Path:      path,
			MediaType: "image/png",
		}, ImageSpec{WidthCells: 2, HeightCells: 1, Fallback: "😀"})
		if !errors.Is(err, ErrImagePreparationFailed) {
			t.Fatalf("PrepareImage error = %v, want ErrImagePreparationFailed", err)
		}
		if strings.Contains(err.Error(), path) {
			t.Fatalf("PrepareImage error leaked source path: %v", err)
		}
	})
}

func TestPreparedImageRendererFailurePreservesFallbackCell(t *testing.T) {
	spec := ImageSpec{WidthCells: 6, HeightCells: 1, Fallback: "Kappa"}
	renderer := &PreparedImageRenderer{
		Preparer: failingImagePreparer{err: ErrImagePreparationFailed},
		Renderer: NewKittyRenderer(supportedKittyDecision()),
	}

	cell, err := renderer.RenderImage(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:      "missing.png",
		MediaType: "image/png",
	}, spec)
	if !errors.Is(err, ErrImagePreparationFailed) {
		t.Fatalf("RenderImage error = %v, want ErrImagePreparationFailed", err)
	}
	if cell.WidthCells != 6 {
		t.Fatalf("cell.WidthCells = %d, want 6", cell.WidthCells)
	}
	if got, want := cell.Text, "Kappa "; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
}

func TestPreparedImageRendererDelegateFailurePreservesFallbackCell(t *testing.T) {
	spec := ImageSpec{WidthCells: 6, HeightCells: 1, Fallback: "Kappa"}
	renderer := &PreparedImageRenderer{
		Renderer: failingImageRenderer{
			cell: ImageCell{Text: "x", WidthCells: 1},
			err:  ErrImageRenderFailed,
		},
	}

	cell, err := renderer.RenderImage(context.Background(), storage.AssetRecord{
		Key:       storage.AssetKey{Kind: "twitch_emote", ID: "25"},
		Path:      writeTinyPNG(t),
		MediaType: "image/png",
	}, spec)
	if !errors.Is(err, ErrImageRenderFailed) {
		t.Fatalf("RenderImage error = %v, want ErrImageRenderFailed", err)
	}
	if cell.WidthCells != 6 {
		t.Fatalf("cell.WidthCells = %d, want 6", cell.WidthCells)
	}
	if got, want := cell.Text, "Kappa "; got != want {
		t.Fatalf("cell.Text = %q, want fallback %q", got, want)
	}
}

func TestImageCellKeyForRefRejectsCredentialLikeIdentity(t *testing.T) {
	unsafeRefs := []twitch.AssetRef{
		{Kind: "avatar", ID: "https://cdn.example/avatar.png?access_token=secret"},
		{Kind: "oauth:secret", ID: "user-1"},
		{Kind: "emoji", ID: "client_secret=secret"},
	}
	for _, ref := range unsafeRefs {
		if key, ok := ImageCellKeyForRef(ref); ok {
			t.Fatalf("ImageCellKeyForRef(%#v) = %#v, true; want false", ref, key)
		}
	}

	key, ok := ImageCellKeyForRef(twitch.AssetRef{Kind: "badge", ID: "subscriber/12"})
	if !ok || key != (ImageCellKey{Kind: "badge", ID: "subscriber/12"}) {
		t.Fatalf("safe badge key = %#v ok=%v, want subscriber/12 true", key, ok)
	}
}

func supportedKittyDecision() ImageCapabilityDecision {
	return ImageCapabilityDecision{
		Status:      ImageCapabilityEnabled,
		EnableKitty: true,
		Signals: TerminalImageSignals{
			KittyCompatible: true,
			KittyWindowID:   "42",
			TrueColor:       true,
		},
	}
}

type failingImagePreparer struct {
	err error
}

func (p failingImagePreparer) PrepareImage(context.Context, storage.AssetRecord, ImageSpec) (storage.AssetRecord, error) {
	return storage.AssetRecord{}, p.err
}

type failingImageRenderer struct {
	cell ImageCell
	err  error
}

func (r failingImageRenderer) RenderImage(context.Context, storage.AssetRecord, ImageSpec) (ImageCell, error) {
	return r.cell, r.err
}

func writeTinyPNG(t *testing.T) string {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/p9sAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatalf("decode fixture PNG: %v", err)
	}
	path := filepath.Join(t.TempDir(), "asset.png")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write fixture PNG: %v", err)
	}
	return path
}

func writePNGImageFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset.png")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create PNG fixture: %v", err)
	}
	defer file.Close()
	if err := png.Encode(file, gradientFixtureImage()); err != nil {
		t.Fatalf("encode PNG fixture: %v", err)
	}
	return path
}

func writeJPEGImageFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset.jpg")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create JPEG fixture: %v", err)
	}
	defer file.Close()
	if err := jpeg.Encode(file, gradientFixtureImage(), &jpeg.Options{Quality: 90}); err != nil {
		t.Fatalf("encode JPEG fixture: %v", err)
	}
	return path
}

func writeGIFImageFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "asset.gif")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create GIF fixture: %v", err)
	}
	defer file.Close()
	palette := color.Palette{color.Black, color.White, color.RGBA{R: 0x91, G: 0x46, B: 0xff, A: 0xff}}
	img := image.NewPaletted(image.Rect(0, 0, 40, 20), palette)
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.SetColorIndex(x, y, uint8((x+y)%len(palette)))
		}
	}
	if err := gif.Encode(file, img, nil); err != nil {
		t.Fatalf("encode GIF fixture: %v", err)
	}
	return path
}

func gradientFixtureImage() image.Image {
	img := image.NewRGBA(image.Rect(0, 0, 40, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 40; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x * 4), G: uint8(y * 8), B: 0x80, A: 0xff})
		}
	}
	return img
}
