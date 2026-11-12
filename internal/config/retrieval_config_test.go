package config

import "testing"

func TestRetrievalSettingsDefaults(t *testing.T) {
	cfg, err := loadWelcome(t, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	idf, ood, theta := cfg.RetrievalSettings()
	if !idf || !ood || theta != 0.60 {
		t.Fatalf("defaults = %v %v %v", idf, ood, theta)
	}
}

func TestRetrievalThetaValidation(t *testing.T) {
	if _, err := loadWelcome(t, `{"retrieval":{"ood_theta":1.5}}`); err == nil {
		t.Fatal("out-of-range theta accepted")
	}
}

func TestRetrievalEnvOverrides(t *testing.T) {
	cfg, err := loadWelcome(t, `{"retrieval":{"idf_veto":false,"ood_guard":false,"ood_theta":0.2}}`)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("SILA_IDF_VETO", "on")
	t.Setenv("SILA_OOD_GUARD", "on")
	idf, ood, theta := cfg.RetrievalSettings()
	if !idf || !ood || theta != 0.2 {
		t.Fatalf("env on = %v %v %v", idf, ood, theta)
	}
	t.Setenv("SILA_IDF_VETO", "off")
	t.Setenv("SILA_OOD_GUARD", "off")
	idf, ood, _ = cfg.RetrievalSettings()
	if idf || ood {
		t.Fatalf("env off = %v %v", idf, ood)
	}
}
