//go:build !webp

package files

import (
    "fmt"
    "image"
    "io"
)

// tryDecodeWebP is a no-op when WebP support is disabled; returns an error.
func tryDecodeWebP(r io.Reader) (image.Image, error) {
    return nil, fmt.Errorf("webp support disabled")
}

// encodeWebP is a no-op when WebP support is disabled; returns an error.
func encodeWebP(w io.Writer, img image.Image) error {
    return fmt.Errorf("webp support disabled")
}

