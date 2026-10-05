package aws

import "encoding/json"

// stringOrSlice unmarshals an IAM field that may be a single string or a list
// of strings into a []string.
type stringOrSlice []string

// UnmarshalJSON implements json.Unmarshaler.
func (s *stringOrSlice) UnmarshalJSON(data []byte) error {
	// Try a single string first.
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*s = []string{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*s = list
	return nil
}
