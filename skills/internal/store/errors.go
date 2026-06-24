package store

import "errors"

var (
	ErrNotFound        = errors.New("skill package not found")
	ErrInvalidPath     = errors.New("invalid skill file path")
	ErrInvalidSlug     = errors.New("invalid skill slug")
	ErrInvalidPackage  = errors.New("invalid skill package")
	ErrFileTooLarge    = errors.New("skill file too large")
	ErrPackageTooLarge = errors.New("skill package too large")
)
