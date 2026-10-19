//go:build !windows

package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

func Endpoint() (string, error) { d, e := DataDir(); return filepath.Join(d, "broker.sock"), e }
func Protect(path string) error {
	s, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if s.Mode()&os.ModeSymlink != 0 {
		return errors.New("symlink denied")
	}
	m := os.FileMode(0600)
	if s.IsDir() {
		m = 0700
	}
	return os.Chmod(path, m)
}
func Check(path string) error {
	s, e := os.Lstat(path)
	if e != nil {
		return e
	}
	var st unix.Stat_t
	if e = unix.Lstat(path, &st); e != nil {
		return e
	}
	if int(st.Uid) != os.Getuid() || s.Mode().Perm()&0077 != 0 || s.Mode()&os.ModeSymlink != 0 {
		return errors.New("unsafe owner or permissions")
	}
	return nil
}
func Replace(from, to string) error { return os.Rename(from, to) }
func SyncDir(path string) error {
	f, e := os.Open(path)
	if e != nil {
		return e
	}
	defer f.Close()
	return f.Sync()
}
func Lock(dir string) (func(), error) {
	p := filepath.Join(dir, "broker.lock")
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = Check(p); e == nil {
		e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	}
	if e != nil {
		f.Close()
		return nil, errors.New("broker already running or unsafe lock")
	}
	return func() { unix.Flock(int(f.Fd()), unix.LOCK_UN); f.Close() }, nil
}
func Listen(endpoint string) (net.Listener, error) {
	if e := Check(filepath.Dir(endpoint)); e != nil {
		return nil, e
	}
	if _, e := os.Lstat(endpoint); e == nil {
		if e = Check(endpoint); e != nil {
			return nil, e
		}
		if e = os.Remove(endpoint); e != nil {
			return nil, e
		}
	}
	l, e := net.Listen("unix", endpoint)
	if e != nil {
		return nil, e
	}
	if e = Protect(endpoint); e != nil {
		l.Close()
		return nil, e
	}
	return l, nil
}
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	if e := Check(filepath.Dir(endpoint)); e != nil {
		return nil, e
	}
	if e := Check(endpoint); e != nil {
		return nil, e
	}
	return (&net.Dialer{}).DialContext(ctx, "unix", endpoint)
}
func KeyringUser() string { return "user-" + strconv.Itoa(os.Getuid()) }
