package domain

import "errors"

// ErrContentIntegrity means stored bytes failed their immutable identity check.
// Submitted-input validation has separate package/hash errors.
var ErrContentIntegrity = errors.New("stored content integrity failure")

// Query rows retain the durable list ordering key independently of the item's
// semantic revision and validator.
type QueryProject struct {
	Project
	UpdatedAt string
}

type QueryDocument struct {
	Document
	UpdatedAt string
}
