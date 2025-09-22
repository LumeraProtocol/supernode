//go:build webp

package files

import (
    "image"
    "io"

    "github.com/kolesa-team/go-webp/decoder"
    "github.com/kolesa-team/go-webp/encoder"
    "github.com/kolesa-team/go-webp/webp"
)

// tryDecodeWebP attempts to decode a WebP image from the provided reader.
func tryDecodeWebP(r io.Reader) (image.Image, error) {
    return webp.Decode(r, &decoder.Options{})
}

// encodeWebP encodes the provided image as WebP to the writer.
func encodeWebP(w io.Writer, img image.Image) error {
    opts, err := encoder.NewLosslessEncoderOptions(encoder.PresetDefault, 0)
    if err != nil {
        return err
    }
    return webp.Encode(w, img, opts)
}
