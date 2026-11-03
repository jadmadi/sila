package render

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestNormalizeSlicesReplacesNilCollections(t *testing.T) {
	type inner struct {
		Items []string `json:"items"`
	}
	type payload struct {
		List    []string          `json:"list"`
		Nested  inner             `json:"nested"`
		Pointer *inner            `json:"pointer"`
		Map     map[string]any    `json:"map"`
		Numbers []int             `json:"numbers"`
		Any     any               `json:"any"`
		Named   []json.RawMessage `json:"named"`
	}

	in := payload{
		Pointer: &inner{},
		Map:     map[string]any{"list": []string(nil)},
		Any:     inner{},
	}
	out := NormalizeSlices(in).(payload)
	if out.List == nil || len(out.List) != 0 {
		t.Fatalf("top-level nil slice was not normalized: %#v", out.List)
	}
	if out.Nested.Items == nil {
		t.Fatal("nested nil slice was not normalized")
	}
	if out.Pointer == nil || out.Pointer.Items == nil {
		t.Fatal("pointer nil slice was not normalized")
	}
	if out.Map["list"] == nil {
		t.Fatal("map value nil slice was not normalized")
	}
	if out.Numbers == nil {
		t.Fatal("typed nil slice was not normalized")
	}
	if out.Any.(inner).Items == nil {
		t.Fatal("interface value nil slice was not normalized")
	}
	if out.Named == nil {
		t.Fatal("named nil slice was not normalized")
	}
}

func TestNormalizeSlicesPreservesNonNilValues(t *testing.T) {
	in := struct {
		Items []string `json:"items"`
	}{Items: []string{"a"}}
	out := NormalizeSlices(in).(struct {
		Items []string `json:"items"`
	})
	if len(out.Items) != 1 || out.Items[0] != "a" {
		t.Fatalf("non-nil slice changed: %#v", out.Items)
	}
}

func TestEnvelopeShapes(t *testing.T) {
	good := NewSuccessEnvelope(map[string]any{"count": 0}, nil, nil)
	if good.Schema != JSONSchemaV1 || !good.OK || good.Error != nil {
		t.Fatalf("bad success envelope: %#v", good)
	}
	if good.Warnings == nil || good.Mutations == nil {
		t.Fatal("success envelope collections must be non-nil")
	}

	bad := NewErrorEnvelope("GOAL_NOT_FOUND", "missing", "sila goals")
	if bad.OK || bad.Error == nil || bad.Error.Code != "GOAL_NOT_FOUND" {
		t.Fatalf("bad error envelope: %#v", bad)
	}

	var buf bytes.Buffer
	if err := EmitJSONSuccess(&buf, map[string]any{"items": []string(nil)}, []string{"w"}, []string{"goals/README.md"}); err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(buf.Bytes(), &decoded); err != nil {
		t.Fatalf("envelope is not valid JSON: %v", err)
	}
	if decoded["ok"] != true {
		t.Fatalf("ok = %v", decoded["ok"])
	}
	data := decoded["data"].(map[string]any)
	if data["items"] == nil {
		t.Fatal("envelope data list is null")
	}
	mutations := decoded["mutations"].([]any)
	if len(mutations) != 1 || mutations[0] != "goals/README.md" {
		t.Fatalf("mutations = %#v", mutations)
	}
}
