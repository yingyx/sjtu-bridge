package canvas

import (
	"github.com/yingyx/sjtu-bridge/contracts"
	"mime"
	"net/url"
	"strings"
)

func scopedNext(headers []string, current *url.URL, allowed func(*url.URL) bool) (*url.URL, error) {
	var next *url.URL
	bad := func() (*url.URL, error) { return nil, contracts.Fail("UPSTREAM_CHANGED", "Invalid scoped pagination") }
	for _, h := range headers {
		for _, link := range strings.Split(h, ",") {
			p := strings.Split(link, ";")
			if len(p) < 2 {
				return bad()
			}
			_, params, e := mime.ParseMediaType("application/link;" + strings.Join(p[1:], ";"))
			if e != nil || params["rel"] == "" {
				return bad()
			}
			isNext := false
			for _, rel := range strings.Fields(params["rel"]) {
				if rel == "next" {
					isNext = true
				}
			}
			if !isNext {
				continue
			}
			target := strings.TrimSpace(p[0])
			if !strings.HasPrefix(target, "<") || !strings.HasSuffix(target, ">") || next != nil {
				return bad()
			}
			n, e := url.Parse(target[1 : len(target)-1])
			if e != nil {
				return bad()
			}
			n = current.ResolveReference(n)
			if !allowed(n) {
				return bad()
			}
			next = n
		}
	}
	return next, nil
}
