// Package local contains IPC and filesystem primitives, never vault access.
package local

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
)

func Random() string {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("system random source unavailable")
	}
	return hex.EncodeToString(b[:])
}
func DataDir() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "sjtu-bridge"), nil
}
func SecureDir(path string) error {
	if err := os.MkdirAll(path, 0700); err != nil {
		return err
	}
	return Protect(path)
}

// WriteNew creates a private file without replacing any existing delivery.
func WriteNew(path string, b []byte) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute path required")
	}
	if err := Check(filepath.Dir(path)); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	ok := false
	defer func() {
		f.Close()
		if !ok {
			os.Remove(path)
		}
	}()
	if err = Protect(path); err != nil {
		return err
	}
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = SyncDir(filepath.Dir(path)); err != nil {
		return err
	}
	ok = true
	return nil
}

// Atomic replaces a snapshot only after a complete flushed private temp file.
func Atomic(path string, b []byte) error {
	tmp := path + "." + Random() + ".tmp"
	if err := WriteNew(tmp, b); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err := Replace(tmp, path); err != nil {
		return err
	}
	return SyncDir(filepath.Dir(path))
}
