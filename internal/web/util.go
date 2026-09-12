package web

import (
	"bytes"
	"encoding/json"
)

func jsonInto(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}

// prettyJSON re-indents stored JSON for download; the input is returned
// unchanged if it does not parse.
func prettyJSON(s string) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return []byte(s)
	}
	buf.WriteByte('\n')
	return buf.Bytes()
}
