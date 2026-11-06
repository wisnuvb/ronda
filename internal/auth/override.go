package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/wisnuvb/ronda/i18n"
)

const overrideFile = "auth.json"

// The config file is read-only to the service, so a password changed from the dashboard is kept here
// instead, next to the data. The file is tied to the hash it replaced: edit auth.password_hash in the
// config (for example with install.sh --reset-password) and the stored override stops applying by
// itself, so an administrator at the console always has the last word.
type override struct {
	PasswordHash      string `json:"password_hash"`
	ConfigFingerprint string `json:"config_fingerprint"`
}

func fingerprint(configHash string) string {
	sum := sha256.Sum256([]byte(configHash))
	return hex.EncodeToString(sum[:])
}

// LoadOverride returns the hash saved by SaveOverride, if there is one and it still belongs to configHash.
func LoadOverride(dir, configHash string) (string, bool) {
	if dir == "" {
		return "", false
	}
	b, err := os.ReadFile(filepath.Join(dir, overrideFile))
	if err != nil {
		return "", false
	}
	var o override
	if json.Unmarshal(b, &o) != nil || o.ConfigFingerprint != fingerprint(configHash) || ValidHash(o.PasswordHash) != nil {
		return "", false
	}
	return o.PasswordHash, true
}

// SaveOverride stores newHash as the dashboard password, atomically and readable by the service only.
func SaveOverride(dir, configHash, newHash string) error {
	if dir == "" {
		return errors.New(i18n.T("Data directory is not set"))
	}
	if err := ValidHash(newHash); err != nil {
		return err
	}
	b, err := json.Marshal(override{PasswordHash: newHash, ConfigFingerprint: fingerprint(configHash)})
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, overrideFile+".tmp-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), filepath.Join(dir, overrideFile))
}
