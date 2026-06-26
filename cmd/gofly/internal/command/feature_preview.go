package command

import "sort"

type featureListPreview struct {
	Features []string `json:"features"`
}

type featureRunPreview struct {
	Features []string             `json:"features"`
	Files    []featureFilePreview `json:"files"`
	Data     []featureDataPreview `json:"data,omitempty"`
}

type featureFilePreview struct {
	Path  string `json:"path"`
	Bytes int    `json:"bytes"`
}

type featureDataPreview struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

func buildFeatureRunPreview(names []string, files map[string]string, data map[string]string) featureRunPreview {
	preview := featureRunPreview{
		Features: append([]string(nil), names...),
		Files:    make([]featureFilePreview, 0, len(files)),
		Data:     make([]featureDataPreview, 0, len(data)),
	}
	filePaths := make([]string, 0, len(files))
	for path := range files {
		filePaths = append(filePaths, path)
	}
	sort.Strings(filePaths)
	for _, path := range filePaths {
		preview.Files = append(preview.Files, featureFilePreview{Path: path, Bytes: len(files[path])})
	}
	dataKeys := make([]string, 0, len(data))
	for key := range data {
		dataKeys = append(dataKeys, key)
	}
	sort.Strings(dataKeys)
	for _, key := range dataKeys {
		preview.Data = append(preview.Data, featureDataPreview{Key: key, Value: data[key]})
	}
	return preview
}
