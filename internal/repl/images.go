package repl

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
)

type Image struct {
	Path string `json:"path,omitempty"`
	URL  string `json:"url"`
}

// CaptureImage validates and embeds image bytes so later file changes do not affect delivery.
func CaptureImage(path string) (Image, error) {
	f, err := os.Open(path)
	if err != nil {
		return Image{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return Image{}, err
	}
	if !info.Mode().IsRegular() {
		return Image{}, fmt.Errorf("view requires a regular image file")
	}
	data, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
	if err != nil {
		return Image{}, err
	}
	if len(data) > 2*1024*1024 {
		return Image{}, fmt.Errorf("view image exceeds 2 MiB")
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return Image{}, fmt.Errorf("view requires a PNG or JPEG image: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || int64(cfg.Width)*int64(cfg.Height) > 20_000_000 {
		return Image{}, fmt.Errorf("view image exceeds 20 megapixels")
	}
	return Image{URL: "data:image/" + format + ";base64," + base64.StdEncoding.EncodeToString(data)}, nil
}
