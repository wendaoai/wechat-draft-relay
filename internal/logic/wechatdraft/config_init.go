package wechatdraft

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
)

// InitConfig creates a private local key once; it never overwrites a configured
// service, prints its key, or stores any user's WeChat AppSecret.
func InitConfig(path string) error {
	path, err := ConfigFilePath(path)
	if err != nil {
		return err
	}
	guard := Store{config: Config{DataDir: filepath.Dir(path)}}
	if guard.safe(path) != nil {
		return errors.New("unsafe configuration path")
	}
	key := make([]byte, 32)
	if _, err = rand.Read(key); err != nil {
		return errors.New("cannot generate service key")
	}
	b := []byte(defaultEnvText(base64.RawURLEncoding.EncodeToString(key)))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return errors.New("configuration already exists or directory is not writable; no file overwritten")
	}
	saved := false
	defer func() {
		f.Close()
		if !saved {
			_ = os.Remove(path)
		}
	}()
	if err = restrictConfigFile(path); err == nil {
		_, err = f.Write(b)
	}
	if err == nil {
		err = f.Sync()
	}
	if err == nil {
		err = f.Close()
	}
	if err == nil {
		err = syncDirectory(filepath.Dir(path))
	}
	if err != nil {
		return errors.New("cannot safely save private configuration")
	}
	saved = true
	return nil
}
