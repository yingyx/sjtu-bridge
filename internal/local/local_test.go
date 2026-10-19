package local

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestPrivateFilesAndSingleton(t *testing.T) {
	d := t.TempDir()
	if e := Protect(d); e != nil {
		t.Fatal(e)
	}
	if e := Check(d); e != nil {
		t.Fatal(e)
	}
	p := filepath.Join(d, "snapshot")
	if e := WriteNew(p, []byte("synthetic")); e != nil {
		t.Fatal(e)
	}
	if e := Check(p); e != nil {
		t.Fatal(e)
	}
	if e := WriteNew(p, []byte("replace")); e == nil {
		t.Fatal("overwrote delivery")
	}
	if e := Atomic(p, []byte("next")); e != nil {
		t.Fatal(e)
	}
	if e := Check(p); e != nil {
		t.Fatal(e)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "next" {
		t.Fatal("atomic replacement")
	}
	u, e := Lock(d)
	if e != nil {
		t.Fatal(e)
	}
	if second, e := Lock(d); e == nil {
		second()
		t.Fatal("second writer admitted")
	}
	u()
	u, e = Lock(d)
	if e != nil {
		t.Fatal(e)
	}
	u()
}
func TestNativeIPC(t *testing.T) {
	d := t.TempDir()
	if e := Protect(d); e != nil {
		t.Fatal(e)
	}
	endpoint := filepath.Join(d, "test.sock")
	if runtime.GOOS == "windows" {
		endpoint = `\\.\pipe\sjtu-bridge-test-` + Random()
	}
	l, e := Listen(endpoint)
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	errch := make(chan error, 1)
	go func() {
		c, e := l.Accept()
		if e != nil {
			errch <- e
			return
		}
		defer c.Close()
		c.SetDeadline(time.Now().Add(3 * time.Second))
		_, e = c.Write([]byte("synthetic-ipc"))
		errch <- e
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, e := Dial(ctx, endpoint)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	b, e := io.ReadAll(c)
	if e != nil || string(b) != "synthetic-ipc" {
		t.Fatal(e)
	}
	if e = <-errch; e != nil {
		t.Fatal(e)
	}
}
