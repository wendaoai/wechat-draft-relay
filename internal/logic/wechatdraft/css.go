package wechatdraft

import (
	"io"
	"regexp"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/css"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

var cssComments = regexp.MustCompile(`(?s)/\*.*?\*/`)
var cssResourceFunction = regexp.MustCompile(`(?i)(url|expression|image-set|-webkit-image-set|image|src|paint|attr)\s*\(`)

// Accept inline layout declarations without a property/value whitelist.
// Resource loading and executable legacy CSS remain invalid, as in the desktop.
func validStyle(value string) bool {
	if strings.ContainsAny(value, "\\@<>\x00") {
		return false
	}
	// Custom property values are emitted by the parser as one raw token. Inspect
	// those too so a var() cannot conceal an external resource declaration.
	if cssResourceFunction.MatchString(cssComments.ReplaceAllString(value, "")) {
		return false
	}
	p := css.NewParser(parse.NewInputString(value), true)
	for {
		grammar, _, property := p.Next()
		if grammar == css.ErrorGrammar {
			return p.Err() == io.EOF
		}
		name := strings.ToLower(string(property))
		if (grammar != css.DeclarationGrammar && grammar != css.CustomPropertyGrammar) || name == "behavior" || name == "-moz-binding" || len(p.Values()) == 0 {
			return false
		}
		for _, token := range p.Values() {
			if token.TokenType == css.URLToken {
				return false
			}
			if token.TokenType == css.FunctionToken {
				switch strings.ToLower(string(token.Data)) {
				case "url(", "expression(", "image-set(", "-webkit-image-set(", "image(", "src(", "paint(", "attr(":
					return false
				}
			}
		}
	}
}

func parseFragment(source string) ([]*html.Node, error) {
	return html.ParseFragment(strings.NewReader(source), &html.Node{Type: html.ElementNode, Data: "div", DataAtom: atom.Div})
}
