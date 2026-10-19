// Package testkit provides only deterministic synthetic campus data.
package testkit

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/yingyx/sjtu-bridge/internal/domain"
	"github.com/zalando/go-keyring"
)

type Keyring struct {
	mu     sync.Mutex
	Values map[string]string
	Fail   bool
}

func (k *Keyring) Get(s, u string) (string, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Fail {
		return "", keyring.ErrUnsupportedPlatform
	}
	v, ok := k.Values[s+u]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}
func (k *Keyring) Set(s, u, v string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.Fail {
		return keyring.ErrUnsupportedPlatform
	}
	if k.Values == nil {
		k.Values = map[string]string{}
	}
	k.Values[s+u] = v
	return nil
}
func (k *Keyring) Delete(s, u string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	delete(k.Values, s+u)
	return nil
}

type Canvas struct {
	Calls      atomic.Int32
	Account    string
	Read       func(context.Context, string, int, string) (domain.AssignmentPage, error)
	CourseRead func(context.Context) error
}

func (f *Canvas) Identity(context.Context, []byte) (string, error) {
	if f.Account != "" {
		return f.Account, nil
	}
	return "canvas:user:7", nil
}
func (f *Canvas) Courses(ctx context.Context, _ []byte, _ string) ([]domain.CanvasCourse, error) {
	if f.CourseRead != nil {
		if e := f.CourseRead(ctx); e != nil {
			return nil, e
		}
	}
	return []domain.CanvasCourse{{Ref: "canvas:course:1", Title: "同名课程"}, {Ref: "canvas:course:2", Title: "同名课程"}}, nil
}
func (f *Canvas) Assignments(ctx context.Context, _ []byte, account, course string, limit int, next string) (domain.AssignmentPage, error) {
	f.Calls.Add(1)
	if f.Read != nil {
		return f.Read(ctx, course, limit, next)
	}
	return Page(account, course, next), nil
}
func Page(account, course, next string) domain.AssignmentPage {
	id, title := "canvas:assignment:10", "合成作业标题"
	continuation := "page-2"
	if next != "" {
		id, title, continuation = "canvas:assignment:11", "第二页合成标题", ""
	}
	return domain.AssignmentPage{Rows: []domain.AssignmentRow{{AccountRef: account, CourseRef: course, Ref: id, Title: title}}, Next: continuation}
}
