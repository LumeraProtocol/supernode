package files

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/LumeraProtocol/supernode/pkg/storage/fs"
	"github.com/stretchr/testify/assert"
)

func Test_StoreFileAfterSetFormat(t *testing.T) {
	storage := NewStorage(fs.NewFileStorage(os.TempDir()))

	files := []struct {
		name   string
		format Format
	}{
		{"test.jpeg", JPEG},
		{"test.jpg", JPEG},
		{"test.png", PNG},
		{"test.webp", WEBP},
	}

	for _, file := range files {
		f := storage.NewFile()
		assert.NotNil(t, f)

		//
		err := f.SetFormatFromExtension(filepath.Ext(file.name))
		assert.Equal(t, nil, err)
		assert.Equal(t, file.format, f.format)

		_, err = storage.File(f.Name())
		assert.Equal(t, nil, err)
	}
}
