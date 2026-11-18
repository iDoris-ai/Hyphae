package storage

import (
	"errors"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/iDoris-ai/hyphae/internal/social/contact"
)

// SendRefusal maps a ReserveSend error to the stable `friend` error codes,
// with the data Agent24 needs to tell the user what is left. Other errors are
// returned unchanged.
func SendRefusal(err error, peer string) error {
	var dl *contact.DailyLimitError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &dl):
		return common.NewExitErrorWithData(common.ErrCodeStrangerDailyLimit, err,
			map[string]any{"used": dl.Used, "limit": contact.DailyLimit, "resets_at": dl.ResetsAt})
	case errors.Is(err, contact.ErrStrangerMessageLimit):
		return common.NewExitErrorWithData(common.ErrCodeStrangerMessageLimit, err,
			map[string]any{"peer": peer, "sent": contact.PerStrangerLimit, "limit": contact.PerStrangerLimit})
	case errors.Is(err, contact.ErrPeerBlocked):
		return common.NewExitErrorWithData(common.ErrCodePeerBlocked, err, map[string]any{"peer": peer})
	}
	return err
}
