package triage

import "testing"

func TestEvalConditionThunderbirdAddressSemantics(t *testing.T) {
	msg := MessageInput{
		From:    "Jira Bot <noreply@acme.de>",
		To:      []string{"Frank Hess <frank@example.org>", "team@example.org"},
		Subject: "AW: Re[2]: Rechnung 4711",
	}
	cases := []struct {
		field, op, value string
		want             bool
	}{
		// From is matched per part (address, display name), not as "Name <addr>".
		{"from", "is", "noreply@acme.de", true},
		{"from", "is", "Jira Bot", true},
		{"from", "endsWith", "@acme.de", true},
		{"from", "endsWith", "acme.de>", false},
		{"from", "beginsWith", "noreply", true},
		{"from", "contains", "Bot <noreply", false},
		{"from", "doesNotContain", "acme", false},
		{"from", "doesNotContain", "example", true},
		{"from", "isNot", "noreply@acme.de", false},
		{"from", "isNot", "other@acme.de", true},
		// To: positive ops match any recipient, negative ops need all recipients.
		{"to", "is", "team@example.org", true},
		{"to", "doesNotContain", "frank", false},
		{"to", "doesNotContain", "nobody", true},
		{"allAddresses", "contains", "acme.de", true},
		// Subject: reply prefixes are normalized to a single "Re: " like Thunderbird.
		{"subject", "beginsWith", "Re: Rechnung", true},
		{"subject", "beginsWith", "AW:", false},
		{"subject", "contains", "Rechnung 4711", true},
		{"subject", "is", "Re: Rechnung 4711", true},
		// Unknown operators never match (previously fell back to contains, inverting doesNotMatch).
		{"subject", "doesNotMatch", "Rechnung", false},
		{"subject", "matches", "Rechnung", false},
	}
	for _, tc := range cases {
		got, hint := evalConditionAgainstMessage(NativeCondition{Field: tc.field, Op: tc.op, Value: tc.value}, msg)
		if got != tc.want {
			t.Errorf("%s %s %q: got %v want %v (hint %q)", tc.field, tc.op, tc.value, got, tc.want, hint)
		}
	}

	if _, hint := evalConditionAgainstMessage(NativeCondition{Field: "subject", Op: "matches", Value: "x"}, msg); hint != "unsupported_operator" {
		t.Fatalf("expected unsupported_operator diagnostic, got %q", hint)
	}
}

func TestEvalConditionEncodedAndUmlautNames(t *testing.T) {
	msg := MessageInput{From: "=?UTF-8?Q?J=C3=BCrgen_M=C3=BCller?= <j@verein.de>"}
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "from", Op: "contains", Value: "Müller"}, msg); !ok {
		t.Fatal("expected decoded display name to match")
	}
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "from", Op: "is", Value: "j@verein.de"}, msg); !ok {
		t.Fatal("expected address to match with is")
	}
	plain := MessageInput{From: "Jürgen Müller <j@verein.de>"}
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "from", Op: "beginsWith", Value: "jürgen"}, plain); !ok {
		t.Fatal("expected UTF-8 display name to match case-insensitively")
	}
	// Bare address without display name.
	bare := MessageInput{From: "billing@hetzner.com"}
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "from", Op: "endsWith", Value: "hetzner.com"}, bare); !ok {
		t.Fatal("expected endsWith on bare address")
	}
	// Empty To list satisfies negative operators only.
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "to", Op: "doesNotContain", Value: "x"}, bare); !ok {
		t.Fatal("expected empty To to satisfy doesNotContain")
	}
	if ok, _ := evalConditionAgainstMessage(NativeCondition{Field: "to", Op: "contains", Value: "x"}, bare); ok {
		t.Fatal("expected empty To not to satisfy contains")
	}
}

func TestThunderbirdFilterSubject(t *testing.T) {
	cases := map[string]string{
		"Rechnung":               "Rechnung",
		"Re: Rechnung":           "Re: Rechnung",
		"AW: RE: Re[3]: Termin":  "Re: Termin",
		"Fwd: Rechnung":          "Fwd: Rechnung",
		"WG: AW: Angebot":        "WG: AW: Angebot",
		"Antw: Frage":            "Re: Frage",
		"  re :  spacing":        "Re: spacing",
		"Reisekosten Abrechnung": "Reisekosten Abrechnung",
	}
	for in, want := range cases {
		if got := ThunderbirdFilterSubject(in); got != want {
			t.Errorf("ThunderbirdFilterSubject(%q)=%q want %q", in, got, want)
		}
	}
	if got := StripSubjectPrefixes("WG: AW: Fwd: Angebot"); got != "Angebot" {
		t.Fatalf("StripSubjectPrefixes: got %q", got)
	}
}
