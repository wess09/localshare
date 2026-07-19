package domain

import "errors"

var (
	ErrUnauthorized       = errors.New("unauthorized")
	ErrNotFound           = errors.New("not found")
	ErrConflict           = errors.New("conflict")
	ErrInvalidInput       = errors.New("invalid input")
	ErrUnavailable        = errors.New("unavailable")
	ErrRouteOnAnotherNode = errors.New("route already active on another node")
)
