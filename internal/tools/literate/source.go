package literate

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

type Source struct {
	Code      string
	DocPath   string
	DocSource string
	LineMap   []Line
}

func (doc *Document) IsProgram() bool {
	_, root := doc.chunks[RootChunk]
	return root || doc.HasFiles()
}

func ReadModule(files fs.FS, path string) (Source, error) {
	if strings.HasSuffix(path, ".fern.md") {
		path = strings.TrimSuffix(path, ".md")
	}
	data, err := fs.ReadFile(files, path)
	if err == nil {
		return Source{Code: string(data)}, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Source{}, err
	}
	docPath := path + ".md"
	data, err = fs.ReadFile(files, docPath)
	if err != nil {
		return Source{}, err
	}
	doc := Parse(string(data))
	if doc.HasFiles() {
		return Source{}, fmt.Errorf("%s: a source document must define one module", docPath)
	}
	code, lines, err := doc.Tangle()
	if err != nil {
		return Source{}, fmt.Errorf("%s: %w", docPath, err)
	}
	return Source{Code: code, DocPath: docPath, DocSource: string(data), LineMap: lines}, nil
}

func ReadFile(path string) ([]byte, error) {
	source, err := ReadModule(os.DirFS(filepath.Dir(path)), filepath.Base(path))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return []byte(source.Code), nil
}
