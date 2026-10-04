package sync

import "errors"

var (
	ErrNotFound               = errors.New("sync medium not found")
	ErrUnknownKind            = errors.New("unknown storage medium kind")
	ErrNoEncryptionKey        = errors.New("sync encryption key is required to store credentials")
	ErrInvalidKey             = errors.New("sync encryption key must be 32 bytes encoded as base64 or hex")
	ErrAttachmentNotSupported = errors.New("attachment sync is not implemented")
	ErrSecretKey              = errors.New("settings must not contain credentials")
	ErrInvalidCiphertext      = errors.New("invalid credential ciphertext")
	ErrMediumIDRequired       = errors.New("medium id is required")
	ErrMediumNameRequired     = errors.New("medium name is required")
	ErrMediumKindRequired     = errors.New("medium kind is required")
	ErrEngineNotConfigured    = errors.New("sync engine is not configured")
	ErrMediumDisabled         = errors.New("storage medium is disabled")
)
