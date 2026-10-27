package groupchat

import (
	"database/sql"
	"errors"
	"fmt"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/identity"
)

// userInputError reports caller-input failures as user_error in --json mode.
// Only a closed set of known sentinels is promoted; every other error (SQL
// failures, store corruption, protocol mismatches, partial fanouts) is
// returned untouched so it keeps the other_error class.
func userInputError(err error) error {
	if err == nil {
		return nil
	}
	var exit *common.ExitError
	if errors.As(err, &exit) {
		return err
	}
	var missingIdentity *identity.IdentityNotFoundError
	if errors.Is(err, ErrGroupNotFound) || errors.Is(err, ErrInviteNotFound) ||
		errors.Is(err, ErrGroupNotActive) || errors.Is(err, ErrGroupLeft) ||
		errors.As(err, &missingIdentity) {
		return common.NewExitError(common.ErrCodeUser, err)
	}
	return err
}

// requireGroupID rejects a group id that is not a well-formed opaque id before
// any store access, so a typo is a user error instead of a store error.
func requireGroupID(id string) error {
	if !validOpaqueID(id) {
		return common.NewExitError(common.ErrCodeUser, errors.New("invalid group ID"))
	}
	return nil
}

// requireLogicalID is requireGroupID for the logical send id.
func requireLogicalID(id string) error {
	if !validOpaqueID(id) {
		return common.NewExitError(common.ErrCodeUser, errors.New("invalid logical message ID"))
	}
	return nil
}

// unknownLogicalError turns a missing fanout (sql.ErrNoRows from
// LoadFanoutReport) into a user_error that names the logical id; any other
// error is returned as is.
func unknownLogicalError(err error, key FanoutKey) error {
	if errors.Is(err, sql.ErrNoRows) {
		return common.NewExitError(common.ErrCodeUser, fmt.Errorf("logical id %s not found in group %s", key.SendKey, key.GroupID))
	}
	return err
}
