package identity

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/iDoris-ai/hyphae/internal/common"
	"github.com/urfave/cli/v3"
)

const (
	passwordCheckSaltBytes       = 16
	passwordCheckMinTokenBytes   = 12 + 16 // GCM nonce plus authentication tag
	passwordCheckSuccessMessage  = "Keystore password is valid"
	passwordCheckFailureMessage  = "password verification failed"
	passwordCheckMissingMessage  = "keystore is not available for password verification"
	passwordCheckMalformedFile   = "keystore file is malformed"
	passwordCheckUnreadableFile  = "keystore file could not be read"
	passwordCheckInvalidEncoding = "keystore password verifier is malformed"
)

type passwordCheckStore struct {
	Encrypted    bool   `json:"encrypted"`
	Salt         string `json:"salt"`
	Verification string `json:"verification"`
}

func checkPasswordCommand() *cli.Command {
	return &cli.Command{
		Name:  "check-password",
		Usage: "Verify the keystore password without changing local data",
		Flags: []cli.Flag{
			&cli.BoolFlag{Name: "password-stdin", Usage: "Read the keystore password from stdin"},
		},
		Action: func(_ context.Context, c *cli.Command) error {
			if !c.Bool("password-stdin") {
				return common.NewExitError(common.ErrCodeUser, errors.New("--password-stdin is required"))
			}
			password, err := readPasswordStdin(os.Stdin)
			if err != nil {
				return common.NewExitError(common.ErrCodeAuth, err)
			}
			if err := verifyKeystorePassword(password); err != nil {
				return err
			}
			common.Emit(common.JSONMode(c), map[string]bool{"valid": true, "encrypted": true}, func() {
				fmt.Println(passwordCheckSuccessMessage)
			})
			return nil
		},
	}
}

// verifyKeystorePassword reads only the persisted verifier fields. It does not
// call any unlock or save path, including the legacy-token migration path.
func verifyKeystorePassword(password string) error {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return common.NewExitError(common.ErrCodeOther, errors.New("home directory is unavailable"))
	}
	storePath := filepath.Join(home, KeyStoreDirName, KeyStoreFile)
	data, err := readRegularKeystoreFile(storePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return common.NewExitError(common.ErrCodeAuth, errors.New(passwordCheckMissingMessage))
		}
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckUnreadableFile))
	}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil || fields == nil {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckMalformedFile))
	}
	var encryptedValue any
	encryptedField, exists := fields["encrypted"]
	if !exists || json.Unmarshal(encryptedField, &encryptedValue) != nil {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckMalformedFile))
	}
	encrypted, exists := encryptedValue.(bool)
	if !exists {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckMalformedFile))
	}
	var ks passwordCheckStore
	if err := json.Unmarshal(data, &ks); err != nil {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckMalformedFile))
	}
	if !encrypted {
		return common.NewExitError(common.ErrCodeAuth, errors.New(passwordCheckMissingMessage))
	}
	salt, err := base64.StdEncoding.Strict().DecodeString(ks.Salt)
	if err != nil || len(salt) != passwordCheckSaltBytes {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckInvalidEncoding))
	}
	verifier, err := base64.StdEncoding.Strict().DecodeString(ks.Verification)
	if err != nil || len(verifier) < passwordCheckMinTokenBytes {
		return common.NewExitError(common.ErrCodeOther, errors.New(passwordCheckInvalidEncoding))
	}
	key, err := deriveMasterKey(password, salt)
	if err != nil {
		return common.NewExitError(common.ErrCodeOther, errors.New("password verification could not be completed"))
	}
	if ok, _ := verifyMasterKey(ks.Verification, key); !ok {
		return common.NewExitError(common.ErrCodeAuth, errors.New(passwordCheckFailureMessage))
	}
	return nil
}
