package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/worxbend/twi/internal/config"
	"github.com/worxbend/twi/internal/render"
	"github.com/worxbend/twi/internal/storage"
	"github.com/worxbend/twi/internal/twitch"
)

func runImageSmoke(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("image-smoke", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var force bool
	fs.BoolVar(&force, "force", false, "emit Kitty graphics even when terminal hints are missing")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cell, err := renderImageSmokeCell(ctx, os.Environ(), force)
	if err != nil {
		fmt.Fprintf(stderr, "image smoke: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, "twi image smoke:")
	fmt.Fprintln(stdout, cell.Text)
	fmt.Fprintln(stdout, "If the terminal supports Kitty graphics, a small color tile should appear above.")
	return 0
}

func renderImageSmokeCell(ctx context.Context, environ []string, force bool) (render.ImageCell, error) {
	signals := render.DetectTerminalImageSignals(environ)
	if force {
		signals.KittyCompatible = true
		signals.TrueColor = true
	}
	if !force && !signals.KittyCompatible {
		return render.ImageCell{}, fmt.Errorf("no Kitty/Ghostty graphics signal; rerun with --force in a known Kitty/Ghostty-compatible terminal to emit a graphics probe")
	}
	features := config.Default().Features
	features.EnableKittyImages = true
	features.ImageMode = "normal"
	decision := render.DecideImageCapabilities(features, signals, true)
	if !force && decision.Status != render.ImageCapabilityEnabled && decision.Status != render.ImageCapabilityDegraded {
		return render.ImageCell{}, fmt.Errorf("%s; rerun with --force in a known Kitty/Ghostty-compatible terminal to emit a graphics probe", decision.Detail)
	}

	dir, err := os.MkdirTemp("", "twi-image-smoke-source-")
	if err != nil {
		return render.ImageCell{}, err
	}
	defer os.RemoveAll(dir)

	sourcePath := filepath.Join(dir, "source.png")
	if err := writeSmokePNG(sourcePath); err != nil {
		return render.ImageCell{}, err
	}

	spec := render.ImageSpec{
		Ref:         twitch.AssetRef{Kind: "image_smoke", ID: "sample"},
		WidthCells:  8,
		HeightCells: 4,
		Fallback:    "[image]",
	}
	source := storage.AssetRecord{
		Key:         storage.AssetKey{Kind: "image_smoke", ID: "sample"},
		Path:        sourcePath,
		MediaType:   "image/png",
		WidthCells:  spec.WidthCells,
		HeightCells: spec.HeightCells,
		FetchedAt:   time.Now(),
	}
	preparer := render.NewPNGImagePreparer(render.ImagePrepareOptions{
		PreparedCache: storage.NewDiskAssetCache(""),
	})
	prepared, err := preparer.PrepareImage(ctx, source, spec)
	if err != nil {
		return render.ImageCell{}, err
	}
	return render.NewKittyRenderer(decision).RenderImage(ctx, prepared, spec)
}

func writeSmokePNG(path string) error {
	const width = 96
	const height = 64
	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.SetNRGBA(x, y, color.NRGBA{
				R: uint8(80 + x*140/width),
				G: uint8(40 + y*180/height),
				B: uint8(210 - x*90/width),
				A: 255,
			})
		}
	}
	for y := 18; y < 46; y++ {
		for x := 26; x < 70; x++ {
			if x < 30 || x >= 66 || y < 22 || y >= 42 {
				img.SetNRGBA(x, y, color.NRGBA{R: 248, G: 248, B: 242, A: 255})
			}
		}
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return png.Encode(file, img)
}
