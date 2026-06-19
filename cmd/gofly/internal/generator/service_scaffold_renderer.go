package generator

import "sort"

type scaffoldRenderedFile struct {
	Path    string
	Content string
}

type serviceScaffoldRenderer struct{}

func (serviceScaffoldRenderer) Render(ir serviceScaffoldIR) []scaffoldRenderedFile {
	keys := make([]string, 0, len(ir.Files))
	for path := range ir.Files {
		keys = append(keys, path)
	}
	sort.Strings(keys)

	files := make([]scaffoldRenderedFile, 0, len(keys))
	for _, path := range keys {
		files = append(files, scaffoldRenderedFile{
			Path:    path,
			Content: render(ir.Files[path], ir.Data),
		})
	}
	return files
}
