package web

import "encoding/json"

func jsonInto(s string, v any) error {
	return json.Unmarshal([]byte(s), v)
}
