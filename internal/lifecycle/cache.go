package lifecycle

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/ldesfontaine/opencloud/internal/fsx"
)

const (
	cacheFileName = "lifecycle.json"
	cacheFileMode = 0o600
)

// FileCache garde la dernière lecture dans le répertoire d'état. Le fichier
// ne contient que des données publiques ; l'effacer coûte une lecture de
// la source.
type FileCache struct {
	root *os.Root
}

func NewFileCache(root *os.Root) *FileCache {
	return &FileCache{root: root}
}

func (c *FileCache) Load() ([]byte, error) {
	content, err := c.root.ReadFile(cacheFileName)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read lifecycle cache: %w", err)
	}
	return content, nil
}

func (c *FileCache) Save(payload []byte) error {
	if err := fsx.WriteAtomic(c.root, cacheFileName, payload, cacheFileMode); err != nil {
		return fmt.Errorf("write lifecycle cache: %w", err)
	}
	return nil
}
