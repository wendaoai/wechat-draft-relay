package wechatdraft

import (
	"io"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
)

// Only declared background images can cross the resource boundary. Validate
// the remaining CSS with the existing policy and preserve its original bytes.
func backgroundStyle(value string, resolve func(string) (string, *APIError)) (string, *APIError) {
	neutral, ok := replaceStyleURLs(value, func(string) (string, bool) { return "none", true })
	if !ok || !validStyle(neutral) {
		return "", htmlFault("不支持的内联样式", "style")
	}
	if neutral == value {
		return value, nil
	}
	replacements := map[string]string{}
	p := css.NewParser(parse.NewInputString(value), true)
	for {
		grammar, _, property := p.Next()
		if grammar == css.ErrorGrammar {
			if p.Err() != io.EOF {
				return "", htmlFault("不支持的内联样式", "style")
			}
			break
		}
		for _, token := range p.Values() {
			if token.TokenType != css.URLToken {
				continue
			}
			name := strings.ToLower(string(property))
			if grammar != css.DeclarationGrammar || (name != "background" && name != "background-image") {
				return "", htmlFault("不支持的内联样式", "style")
			}
			raw := string(token.Data)
			resource := strings.TrimSpace(raw[strings.IndexByte(raw, '(')+1 : len(raw)-1])
			if len(resource) >= 2 && (resource[0] == '\'' || resource[0] == '"') && resource[len(resource)-1] == resource[0] {
				resource = resource[1 : len(resource)-1]
			}
			resolved, e := resolve(resource)
			if e != nil {
				return "", e
			}
			if strings.ContainsAny(resolved, "\"\\<>\r\n\x00") {
				return "", htmlFault("不支持的内联样式", "style")
			}
			replacements[raw] = `url("` + resolved + `")`
		}
	}
	result, ok := replaceStyleURLs(value, func(raw string) (string, bool) {
		replacement, found := replacements[raw]
		return replacement, found
	})
	if !ok {
		return "", htmlFault("不支持的内联样式", "style")
	}
	return result, nil
}

func replaceStyleURLs(value string, replace func(string) (string, bool)) (string, bool) {
	l := css.NewLexer(parse.NewInputString(value))
	var result strings.Builder
	for {
		token, data := l.Next()
		if token == css.ErrorToken {
			return result.String(), l.Err() == io.EOF
		}
		if token == css.URLToken {
			replacement, ok := replace(string(data))
			if !ok {
				return "", false
			}
			result.WriteString(replacement)
		} else {
			result.Write(data)
		}
	}
}
