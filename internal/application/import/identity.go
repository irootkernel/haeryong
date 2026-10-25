package importer

import (
	"errors"
	"fmt"
	"haeryong.local/haeryong/internal/domain"
)

var (
	ErrInputChanged      = errors.New("INPUT_CHANGED")
	ErrHashMismatch      = errors.New("HASH_MISMATCH")
	ErrPackageInvalid    = domain.ErrPackageInvalid
	ErrVersionConflict   = errors.New("VERSION_CONFLICT")
	ErrImmutableConflict = errors.New("IMMUTABLE_VERSION_CONFLICT")
)

func ValidatePath(path string) error               { return domain.ValidatePayloadPath(path) }
func InventoryLine(e domain.Entry) ([]byte, error) { return domain.InventoryLine(e) }
func PathError(path string, err error) error       { return fmt.Errorf("%w: %q", err, path) }
