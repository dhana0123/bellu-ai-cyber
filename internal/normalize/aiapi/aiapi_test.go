package aiapi

import "testing"

func TestMatch(t *testing.T) {
	cases := []struct {
		host    string
		wantOK  bool
		service string
	}{
		{"api.openai.com", true, "OpenAI"},
		{"my-resource.openai.azure.com", true, "Azure OpenAI"},
		{"generativelanguage.googleapis.com", true, "Google Gemini"},
		{"bedrock-runtime.us-east-1.amazonaws.com", true, "AWS Bedrock"},
		{"example.com", false, ""},
		{"API.OPENAI.COM", true, "OpenAI"},
	}
	for _, c := range cases {
		svc, ok := Match(c.host)
		if ok != c.wantOK {
			t.Errorf("Match(%q) ok=%v want %v", c.host, ok, c.wantOK)
			continue
		}
		if ok && svc != c.service {
			t.Errorf("Match(%q) service=%q want %q", c.host, svc, c.service)
		}
	}
}
