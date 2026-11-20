package wechatdraft

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"unicode"
)

func validMediaID(id string) bool {
	return id != "" && len(id) <= 512 && strings.IndexFunc(id, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}

// Detect duplicate top-level fields and trailing objects instead of trusting
// the JSON decoder's last-field-wins behavior for submission evidence.
func responseObject(body []byte) (map[string]json.RawMessage, bool) {
	d := json.NewDecoder(bytes.NewReader(body))
	first, err := d.Token()
	if err != nil || first != json.Delim('{') {
		return nil, false
	}
	obj := map[string]json.RawMessage{}
	for d.More() {
		key, err := d.Token()
		s, ok := key.(string)
		if err != nil || !ok {
			return nil, false
		}
		if _, exists := obj[s]; exists {
			return nil, false
		}
		var raw json.RawMessage
		if d.Decode(&raw) != nil {
			return nil, false
		}
		obj[s] = raw
	}
	last, err := d.Token()
	if err != nil || last != json.Delim('}') {
		return nil, false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return nil, false
	}
	return obj, true
}
