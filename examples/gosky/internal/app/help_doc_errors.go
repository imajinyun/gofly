package app

import "errors"

// Repository sentinels mapped to SmartAdmin-compatible client errors at the
// service boundary. Sentinels live in the app layer so a storage driver never
// imports transport concerns.
var (
	ErrHelpDocNotFound             = errors.New("help document not found")
	ErrHelpDocCatalogNotFound      = errors.New("help doc catalog not found")
	ErrHelpDocCatalogDuplicateName = errors.New("duplicate help doc catalog name")
	ErrHelpDocCatalogHasChildren   = errors.New("help doc catalog has children")
	ErrHelpDocCatalogInUse         = errors.New("help doc catalog contains documents")
	ErrHelpDocCatalogCycle         = errors.New("help doc catalog parent cycle")
)

// ErrOperateLogNotFound is returned when an operate-log row does not exist.
var ErrOperateLogNotFound = errors.New("operate log not found")

// ErrJobNotFound is returned when a scheduled job row does not exist.
var ErrJobNotFound = errors.New("job not found")

// ErrChangeLogNotFound is returned when a release note does not exist.
var ErrChangeLogNotFound = errors.New("change log not found")

// ErrNoticeNotFound is returned when a notice does not exist.
var ErrNoticeNotFound = errors.New("notice not found")
