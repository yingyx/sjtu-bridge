package publicinfo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/yingyx/sjtu-bridge/contracts"
	"github.com/yingyx/sjtu-bridge/internal/transport"
	"golang.org/x/net/html"
)

type Fetcher interface {
	Get(context.Context, string) ([]byte, error)
}
type Provider struct{ fetch Fetcher }

func New(fetch Fetcher) *Provider { return &Provider{fetch: fetch} }

var paths = map[string]string{"calendars": "/jxxl/lnxl.htm", "notices": "/xwtg/tztg.htm"}
var articlePath = regexp.MustCompile(`^/info/[1-9][0-9]{0,5}/[1-9][0-9]{0,9}\.htm$`)
var number = regexp.MustCompile(`^[1-9][0-9]{0,9}$`)

// Public-only locators contain no secrets and convey no authorization. Every
// decoded destination is revalidated; these MUST NOT be reused for private APIs.
type cursor struct {
	Version int    `json:"v"`
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Offset  int    `json:"offset"`
	Limit   int    `json:"limit"`
	Digest  string `json:"digest"`
	Expires int64  `json:"expires"`
}

func encode(c cursor) string { b, _ := json.Marshal(c); return base64.RawURLEncoding.EncodeToString(b) }
func validPath(kind, path string) bool {
	first, ok := paths[kind]
	if !ok {
		return false
	}
	u := &url.URL{Scheme: "https", Host: transport.Host, Path: path}
	return transport.AllowedURL(u) && (path == first || strings.HasPrefix(path, strings.TrimSuffix(first, ".htm")+"/"))
}
func (p *Provider) List(ctx context.Context, kind string, in contracts.ListInput) (contracts.Response, error) {
	if in.Limit < 1 || in.Limit > 100 {
		return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid limit")
	}
	if kind == "services" {
		return services(in)
	}
	path, ok := paths[kind]
	if !ok {
		return contracts.Response{}, contracts.Fail("UNSUPPORTED", "Unknown public list")
	}
	c := cursor{Version: 1, Kind: kind, Path: path, Limit: in.Limit, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(b) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid cursor")
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.Kind != kind || c.Limit != in.Limit || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() || c.Offset < 0 || c.Offset > 100 || !validPath(kind, c.Path) {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Cursor expired or does not match this query")
		}
	}
	source := "https://" + transport.Host + c.Path
	body, err := p.fetch.Get(ctx, source)
	if err != nil {
		return contracts.Response{}, err
	}
	items, next, err := parse(body, source, kind)
	if err != nil {
		return contracts.Response{}, err
	}
	itemJSON, _ := json.Marshal(items)
	sum := sha256.Sum256(itemJSON)
	digest := hex.EncodeToString(sum[:])
	if c.Offset >= len(items) || (c.Offset > 0 && c.Digest != digest) {
		return contracts.Response{}, contracts.Fail("UPSTREAM_CHANGED", "Source page changed; restart the list")
	}
	end := min(c.Offset+in.Limit, len(items))
	result := response(source, items[c.Offset:end])
	if end < len(items) {
		c.Offset = end
		c.Digest = digest
	} else if next != "" {
		c.Path = next
		c.Offset = 0
		c.Digest = ""
	} else {
		return result, nil
	}
	encoded := encode(c)
	result.Meta.NextCursor = &encoded
	result.Meta.HasMore = true
	result.Meta.Sources[0].Coverage = "incomplete"
	result.Warnings = []contracts.Warning{{Code: "MORE_PAGES", Message: "This is one page, not the complete archive. Follow next_cursor with the same limit. The upstream archive can change between calls."}}
	return result, nil
}

func response(source string, items []contracts.Item) contracts.Response {
	return contracts.Response{Data: contracts.Data{Items: items}, Meta: contracts.Meta{
		Completeness: "complete", Sources: []contracts.Source{{Provider: "publicinfo", URL: source, Status: "ok", Coverage: "complete"}},
		FetchedAt: time.Now().UTC(), Timezone: "Asia/Shanghai",
	}, Warnings: []contracts.Warning{}}
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}
func class(n *html.Node, name string) bool {
	for _, c := range strings.Fields(attr(n, "class")) {
		if c == name {
			return true
		}
	}
	return false
}
func find(n *html.Node, match func(*html.Node) bool) *html.Node {
	if n == nil {
		return nil
	}
	if match(n) {
		return n
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if found := find(c, match); found != nil {
			return found
		}
	}
	return nil
}
func text(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.TextNode {
		return n.Data
	}
	if n.Data == "script" || n.Data == "style" {
		return ""
	}
	var s strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		s.WriteString(text(c))
	}
	return s.String()
}
func clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return ' '
		}
		return r
	}, s)), " ")
}
func tag(n *html.Node, t string) *html.Node {
	return find(n, func(n *html.Node) bool { return n.Type == html.ElementNode && n.Data == t })
}
func changed() error {
	return contracts.Fail("UPSTREAM_CHANGED", "Public list structure changed; no complete empty result is available")
}

func articleURL(base *url.URL, href string) (string, bool) {
	ref, err := url.Parse(href)
	if err != nil {
		return "", false
	}
	u := base.ResolveReference(ref)
	if u.Scheme != "https" || u.Host != transport.Host || u.User != nil || u.Fragment != "" || u.RawPath != "" {
		return "", false
	}
	if articlePath.MatchString(u.Path) && u.RawQuery == "" {
		return u.String(), true
	}
	// The site's content.jsp is a public article alias. Canonicalize only these
	// three reviewed parameters; never emit arbitrary or signed query strings.
	if u.Path == "/content.jsp" {
		q, err := url.ParseQuery(u.RawQuery)
		if err == nil && len(q) == 3 && len(q["urltype"]) == 1 && q.Get("urltype") == "news.NewsContentUrl" && len(q["wbtreeid"]) == 1 && len(q["wbnewsid"]) == 1 && number.MatchString(q.Get("wbtreeid")) && number.MatchString(q.Get("wbnewsid")) {
			return "https://" + transport.Host + "/info/" + q.Get("wbtreeid") + "/" + q.Get("wbnewsid") + ".htm", true
		}
	}
	return "", false
}

func parse(body []byte, source, kind string) ([]contracts.Item, string, error) {
	if !utf8.Valid(body) {
		return nil, "", changed()
	}
	root, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return nil, "", changed()
	}
	list := find(root, func(n *html.Node) bool { return class(n, "Newslist") })
	if list == nil {
		return nil, "", changed()
	}
	ul := tag(list, "ul")
	if ul == nil {
		return nil, "", changed()
	}
	base, _ := url.Parse(source)
	items := []contracts.Item{}
	seen := map[string]bool{}
	for li := ul.FirstChild; li != nil; li = li.NextSibling {
		if li.Type != html.ElementNode {
			continue
		}
		if li.Data != "li" {
			return nil, "", changed()
		}
		dateNode := find(li, func(n *html.Node) bool { return class(n, "sj") })
		titleNode := find(li, func(n *html.Node) bool { return class(n, "wz") })
		link := tag(titleNode, "a")
		if link == nil || dateNode == nil {
			return nil, "", changed()
		}
		title := clean(text(tag(link, "h2")))
		destination, ok := articleURL(base, attr(link, "href"))
		if !ok || title == "" || len(title) > 2048 || seen[destination] {
			return nil, "", changed()
		}
		date, err := time.Parse("2006.01.02", clean(text(tag(dateNode, "p")))+"."+clean(text(tag(dateNode, "h2"))))
		if err != nil {
			return nil, "", changed()
		}
		published := date.Format("2006-01-02")
		items = append(items, contracts.Item{Provider: "publicinfo", SourceID: strings.TrimPrefix(destination, "https://"+transport.Host), Title: title, URL: destination, PublishedDate: &published})
		seen[destination] = true
		if len(items) > 100 {
			return nil, "", changed()
		}
	}
	if len(items) == 0 {
		return nil, "", changed()
	}
	pager := find(list, func(n *html.Node) bool { return class(n, "p_pages") })
	if pager == nil {
		return nil, "", changed()
	}
	nextNode := find(pager, func(n *html.Node) bool { return class(n, "p_next") })
	next := ""
	if nextNode != nil {
		a := tag(nextNode, "a")
		if a == nil {
			return nil, "", changed()
		}
		ref, err := url.Parse(attr(a, "href"))
		if err != nil {
			return nil, "", changed()
		}
		u := base.ResolveReference(ref)
		if !transport.AllowedURL(u) || !validPath(kind, u.Path) || u.String() == source {
			return nil, "", changed()
		}
		next = u.Path
	} else if find(pager, func(n *html.Node) bool { return class(n, "p_next_d") }) == nil {
		return nil, "", changed()
	}
	return items, next, nil
}

func services(in contracts.ListInput) (contracts.Response, error) {
	items := directory()
	c := cursor{Version: 1, Kind: "services", Path: directoryRevision, Limit: in.Limit, Expires: time.Now().Add(15 * time.Minute).Unix()}
	if in.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(in.Cursor)
		if err != nil || len(b) > 2048 {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Invalid directory cursor")
		}
		d := json.NewDecoder(bytes.NewReader(b))
		d.DisallowUnknownFields()
		if d.Decode(&c) != nil || d.Decode(new(any)) != io.EOF || c.Version != 1 || c.Kind != "services" || c.Path != directoryRevision || c.Limit != in.Limit || c.Offset < 0 || c.Offset >= len(items) || c.Expires <= time.Now().Unix() || c.Expires > time.Now().Add(15*time.Minute).Unix() {
			return contracts.Response{}, contracts.Fail("INVALID_ARGUMENT", "Directory cursor expired or does not match this query")
		}
	}
	end := min(c.Offset+in.Limit, len(items))
	result := response("https://www.jwc.sjtu.edu.cn/", items[c.Offset:end])
	result.Meta.Completeness = "partial"
	result.Meta.Sources = []contracts.Source{}
	for _, i := range result.Data.Items {
		result.Meta.Sources = append(result.Meta.Sources, contracts.Source{Provider: "publicinfo", URL: i.URL, Status: "bundled", Coverage: "directory_links_only"})
	}
	if end < len(items) {
		c.Offset = end
		next := encode(c)
		result.Meta.HasMore = true
		result.Meta.NextCursor = &next
	}
	result.Warnings = []contracts.Warning{{Code: "BUNDLED_DIRECTORY", Message: "Reviewed 2026-10-20; selected portal locators only, not a complete service inventory or a live availability check. Related capability IDs grant no access."}}
	return result, nil
}
