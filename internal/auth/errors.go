package auth

import "errors"

var (
	errPasswordNotConfigured      = errors.New("password not configured")
	errInvalidPassword            = errors.New("invalid password")
	errPasswordManagementDisabled = errors.New("password management disabled")
	errPasswordSessionRequired    = errors.New("password session required")
	errPasswordTooShort           = errors.New("password must be at least 8 characters")
	errPasswordTooLong            = errors.New("password too long")
)
