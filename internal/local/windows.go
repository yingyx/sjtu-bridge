//go:build windows

package local

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Microsoft/go-winio"
	"golang.org/x/sys/windows"
)

func sid() (string, error) {
	t, e := windows.GetCurrentProcessToken().GetTokenUser()
	if e != nil {
		return "", e
	}
	return t.User.Sid.String(), nil
}
func KeyringUser() string { s, _ := sid(); return s }
func descriptor() (string, error) {
	s, e := sid()
	return "O:" + s + "D:P(A;;FA;;;" + s + ")(A;;FA;;;SY)", e
}
func Endpoint() (string, error) { s, e := sid(); return `\\.\pipe\sjtu-bridge-v1-` + s, e }
func Protect(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("reparse point denied")
	}
	s, e := descriptor()
	if e != nil {
		return e
	}
	sd, e := windows.SecurityDescriptorFromString(s)
	if e != nil {
		return e
	}
	d, _, e := sd.DACL()
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil {
		return e
	}
	return windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, owner, nil, d, nil)
}
func Check(path string) error {
	st, e := os.Lstat(path)
	if e != nil {
		return e
	}
	if st.Mode()&os.ModeSymlink != 0 {
		return errors.New("reparse point denied")
	}
	p, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	a, e := windows.GetFileAttributes(p)
	if e != nil {
		return e
	}
	if a&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return errors.New("reparse point denied")
	}
	sd, e := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil {
		return e
	}
	return checkDescriptor(sd)
}
func checkDescriptor(sd *windows.SECURITY_DESCRIPTOR) error {
	s, e := sid()
	if e != nil {
		return e
	}
	owner, _, e := sd.Owner()
	if e != nil || owner.String() != s {
		return errors.New("unsafe owner")
	}
	// Exact protected allow-list: reject inherited or extra ACEs, including deny ACEs.
	want, e := descriptor()
	if e != nil {
		return e
	}
	expected, e := windows.SecurityDescriptorFromString(want)
	if e != nil {
		return e
	}
	got := sd.String()
	exp := expected.String()
	if !strings.Contains(got, "D:P") || !strings.Contains(got, "(") || got[strings.Index(got, "("):] != exp[strings.Index(exp, "("):] {
		return errors.New("unsafe ACL")
	}
	return nil
}
func Replace(from, to string) error {
	f, e := windows.UTF16PtrFromString(from)
	if e != nil {
		return e
	}
	t, e := windows.UTF16PtrFromString(to)
	if e != nil {
		return e
	}
	return windows.MoveFileEx(f, t, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

// MoveFileEx WRITE_THROUGH is the Windows replacement durability boundary.
func SyncDir(string) error { return nil }
func Lock(dir string) (func(), error) {
	p := filepath.Join(dir, "broker.lock")
	f, e := os.OpenFile(p, os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		return nil, e
	}
	if e = Protect(p); e != nil {
		f.Close()
		return nil, e
	}
	o := &windows.Overlapped{}
	e = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, o)
	if e != nil {
		f.Close()
		return nil, errors.New("broker already running")
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, o); f.Close() }, nil
}
func Listen(endpoint string) (net.Listener, error) {
	s, e := descriptor()
	if e != nil {
		return nil, e
	}
	// go-winio always sets FILE_PIPE_REJECT_REMOTE_CLIENTS.
	return winio.ListenPipe(endpoint, &winio.PipeConfig{SecurityDescriptor: s, InputBufferSize: 4096, OutputBufferSize: 4096})
}
func Dial(ctx context.Context, endpoint string) (net.Conn, error) {
	c, e := winio.DialPipeContext(ctx, endpoint)
	if e != nil {
		return nil, e
	}
	f, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, errors.New("pipe security handle unavailable")
	}
	sd, e := windows.GetSecurityInfo(windows.Handle(f.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e == nil {
		e = checkDescriptor(sd)
	}
	if e != nil {
		c.Close()
		return nil, e
	}
	return c, nil
}
