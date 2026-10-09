package capability

import (
	"strings"
	"testing"
)

// TestCardsExposeNoRawActions asserts capability cards carry human titles and
// descriptions but never raw IAM actions.
func TestCardsExposeNoRawActions(t *testing.T) {
	cards := Cards()
	if len(cards) != 4 {
		t.Fatalf("want 4 cards, got %d", len(cards))
	}
	byName := map[string]Card{}
	for _, c := range cards {
		byName[c.Name] = c
		if c.Title == "" || c.Description == "" {
			t.Errorf("card %q missing title/description", c.Name)
		}
		// No card field should contain an IAM action pattern like "ec2:" or "s3:".
		blob := c.Title + " " + c.Description
		for _, bad := range []string{"ec2:", "s3:", "iam:", "eks:", "lambda:"} {
			if strings.Contains(strings.ToLower(blob), bad) {
				t.Errorf("card %q leaks raw IAM action %q", c.Name, bad)
			}
		}
	}
	if !byName["core"].Default {
		t.Error("core card should be the default selection")
	}
	if byName["eks"].Default {
		t.Error("eks card should not be default")
	}
}
