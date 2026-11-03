package mcp

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

func normalizedJSON(t *testing.T, tool, raw string) string {
	t.Helper()
	out, err := NormalizeArguments(tool, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("NormalizeArguments(%s): %v", tool, err)
	}
	var value map[string]any
	if err := json.Unmarshal(out, &value); err != nil {
		t.Fatalf("normalized output is not an object: %v (%s)", err, out)
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestNormalizeArguments(t *testing.T) {
	cases := []struct {
		name string
		tool string
		raw  string
		want string
	}{
		{"string integer", "recall", `{"query":"busy","limit":"3"}`, `{"limit":3,"query":"busy"}`},
		{"string numbers", "prime_context", `{"max_facts":"3","max_tokens":"2000"}`, `{"max_facts":3,"max_tokens":2000}`},
		{"string boolean", "recall", `{"query":"x","semantic":"true"}`, `{"query":"x","semantic":true}`},
		{"numeric string stays string", "recall", `{"query":"123"}`, `{"query":"123"}`},
		{"double encoded object", "recall", `"{\"query\":\"x\",\"limit\":\"3\"}"`, `{"limit":3,"query":"x"}`},
		{"string array", "save_memo", `{"content":"x","files":"[\"a.go\",\"b.go\"]"}`, `{"content":"x","files":["a.go","b.go"]}`},
		{"unknown tool leaves value", "not_a_tool", `{"limit":"3"}`, `{"limit":"3"}`},
		{"null preserved", "recall", `{"query":"x","limit":null}`, `{"limit":null,"query":"x"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizedJSON(t, tc.tool, tc.raw); got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestNormalizeArgumentsErrors(t *testing.T) {
	cases := []struct {
		name string
		tool string
		raw  string
		want string
	}{
		{"invalid integer", "recall", `{"query":"x","limit":"abc"}`, `integer`},
		{"invalid boolean", "recall", `{"query":"x","semantic":"yes"}`, `boolean`},
		{"non-object", "recall", `[]`, `object`},
		{"malformed", "recall", `{`, `object`},
		{"string-scalar envelope", "recall", `"just a string"`, `object`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NormalizeArguments(tc.tool, json.RawMessage(tc.raw))
			if err == nil {
				t.Fatal("expected an ArgError")
			}
			argErr, ok := err.(*ArgError)
			if !ok {
				t.Fatalf("expected *ArgError, got %T: %v", err, err)
			}
			if !strings.Contains(argErr.Expected, tc.want) {
				t.Fatalf("expected %q in %q", tc.want, argErr.Expected)
			}
		})
	}
}

func TestNormalizeArgumentsBounds(t *testing.T) {
	oversized := `{"query":"` + strings.Repeat("a", MaxToolArgumentsBytes) + `"}`
	if _, err := NormalizeArguments("recall", json.RawMessage(oversized)); err == nil {
		t.Fatal("expected oversized arguments to fail")
	}

	var b strings.Builder
	b.WriteString(`{`)
	for i := 0; i < MaxToolArgumentProperties+1; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`"k`)
		b.WriteString(strconv.Itoa(i))
		b.WriteString(`":"v"`)
	}
	b.WriteString(`}`)
	if _, err := NormalizeArguments("recall", json.RawMessage(b.String())); err == nil {
		t.Fatal("expected too many properties to fail")
	}
}
