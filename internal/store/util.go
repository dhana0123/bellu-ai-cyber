package store

import (
	"encoding/json"
	"strconv"
)

func itoa(n int) string { return strconv.Itoa(n) }

func unmarshalInto(b []byte, dst *map[string]any) error {
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, dst)
}
