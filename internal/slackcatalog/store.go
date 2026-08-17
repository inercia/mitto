package slackcatalog

import (
	"errors"
	"fmt"
	"os"

	"github.com/inercia/mitto/internal/fileutil"
)

type Store interface {
	Load() (document, error)
	Save(document) error
}

type FileStore struct{ Path string }

func NewFileStore(path string) *FileStore { return &FileStore{Path: path} }

func (s *FileStore) Load() (document, error) {
	doc := document{Version: DocumentVersion, Apps: []AppProfile{}, Installations: []Installation{}}
	if err := fileutil.ReadJSON(s.Path, &doc); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return doc, nil
		}
		return document{}, fmt.Errorf("load Slack catalog: %w", err)
	}
	if doc.Version != DocumentVersion {
		return document{}, fmt.Errorf("%w: unsupported document version %d", ErrInvalid, doc.Version)
	}
	if doc.Apps == nil {
		doc.Apps = []AppProfile{}
	}
	if doc.Installations == nil {
		doc.Installations = []Installation{}
	}
	return doc, nil
}

func (s *FileStore) Save(doc document) error {
	doc.Version = DocumentVersion
	if err := fileutil.WriteJSONAtomic(s.Path, &doc, 0o600); err != nil {
		return fmt.Errorf("save Slack catalog: %w", err)
	}
	return nil
}
