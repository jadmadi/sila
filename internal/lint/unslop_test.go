package lint

import (
	"testing"
)

func TestUnslop_SycophancyAndFiller(t *testing.T) {
	input := "You're absolutely right! We will fix the bug.\nGreat question! Here is the answer."
	rep := CheckText(input)
	if rep.Clean {
		t.Fatalf("expected violations for sycophancy")
	}

	cleaned := CleanText(input)
	if CleanText(cleaned) != cleaned {
		t.Errorf("CleanText is not idempotent")
	}

	rep2 := CheckText(cleaned)
	if !rep2.Clean {
		t.Errorf("expected cleaned text to be clean, got %d violations: %+v", len(rep2.Violations), rep2.Violations)
	}
}

func TestUnslop_EmDashesAndBold(t *testing.T) {
	input := "This change—which is critical—will **significantly** improve speed."
	cleaned := CleanText(input)

	if CleanText(cleaned) != cleaned {
		t.Errorf("CleanText is not idempotent")
	}

	rep := CheckText(cleaned)
	if !rep.Clean {
		t.Errorf("expected clean output, got: %s", cleaned)
	}
}

func TestUnslop_Buzzwords(t *testing.T) {
	input := "We will leverage this robust library to streamline the flow prior to launch."
	expected := "We will use this reliable library to simplify the flow before launch."

	cleaned := CleanText(input)
	if cleaned != expected {
		t.Errorf("expected %q, got %q", expected, cleaned)
	}
}

func TestUnslop_PreservesCodeBlocksAndTables(t *testing.T) {
	input := `# Title

Here is code:
` + "```go" + `
func RobustStreamline() string {
	// We leverage em-dash inside code: —
	return "**bold_syntax**"
}
` + "```" + `

And here is a table:
| Name | Description |
| :--- | :--- |
| **Tool** | Robust parser — fast |
`
	cleaned := CleanText(input)
	if cleaned != input {
		t.Errorf("CleanText modified code block or table!\nExpected:\n%s\nGot:\n%s", input, cleaned)
	}
}
