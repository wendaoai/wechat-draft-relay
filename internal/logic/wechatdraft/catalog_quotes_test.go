package wechatdraft

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/wendaoai/wechat-draft-relay/api/wechatdraft/v1"
	"golang.org/x/net/html"
)

// The desktop catalog test can export real relay payloads for this contract
// check. No network, credentials, image upload or draft creation is involved.
func TestDesktopCatalogQuoteCompatibility(t *testing.T) {
	dir := os.Getenv("CW_QUOTE_AUDIT_DIR")
	if dir == "" {
		t.Skip("desktop catalog relay export not requested")
	}
	data, err := os.ReadFile(filepath.Join(dir, "relay-cases.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		ID, Label, HTML string
		URLs            map[string]string
	}
	if err = json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("no exported templates")
	}
	for _, c := range cases {
		t.Run(c.ID, func(t *testing.T) {
			images := make([]v1.Image, 0, len(c.URLs))
			for id := range c.URLs {
				images = append(images, v1.Image{ID: id, Role: "body"})
			}
			prepared, fault := ProcessHTML(c.HTML, images, nil)
			if fault != nil {
				t.Fatal(fault)
			}
			if prepared != c.HTML {
				t.Fatal("relay validation rewrote source")
			}
			final, fault := ProcessHTML(prepared, images, c.URLs)
			if fault != nil {
				t.Fatal(fault)
			}
			if strings.Contains(final, "&#39;") || strings.Contains(final, "&#34;") {
				t.Fatal("incompatible numeric quotes in final relay body")
			}
			before, after := catalogStyles(t, prepared), catalogStyles(t, final)
			if len(before) != len(after) {
				t.Fatal("style count changed")
			}
			for i, s := range before {
				for id, url := range c.URLs {
					s = strings.ReplaceAll(s, "cw-image://"+id, url)
				}
				if s != after[i] {
					t.Fatalf("style %d changed: %s -> %s", i, s, after[i])
				}
			}
		})
	}
}

func catalogStyles(t *testing.T, source string) []string {
	t.Helper()
	nodes, err := parseFragment(source)
	if err != nil {
		t.Fatal(err)
	}
	var result []string
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for _, a := range n.Attr {
			if a.Key == "style" {
				result = append(result, a.Val)
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return result
}
