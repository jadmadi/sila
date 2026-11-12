package config

import (
	"strings"
	"testing"
)

func TestRedactionMaskValidation(t *testing.T) {
	if _, err := loadWelcome(t, `{"redaction":{"mask":"full"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWelcome(t, `{"redaction":{"mask":"prefix"}}`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadWelcome(t, `{"redaction":{"mask":"invalid"}}`); err == nil || !strings.Contains(err.Error(), "redaction.mask") {
		t.Fatalf("err = %v", err)
	}
}
