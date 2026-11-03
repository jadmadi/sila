package assess

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectLicensePermissive(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantLicense string
		wantCat     LicenseCategory
	}{
		{
			name:        "MIT License",
			content:     "MIT License\n\nPermission is hereby granted, free of charge, to any person obtaining a copy...",
			wantLicense: "MIT",
			wantCat:     LicensePermissive,
		},
		{
			name:        "Apache 2.0 License",
			content:     "Apache License, Version 2.0, January 2004\nhttp://www.apache.org/licenses/LICENSE-2.0",
			wantLicense: "Apache-2.0",
			wantCat:     LicensePermissive,
		},
		{
			name:        "BSD-3-Clause License",
			content:     "Redistribution and use in source and binary forms... Neither the name of the copyright holder nor the names...",
			wantLicense: "BSD-3-Clause",
			wantCat:     LicensePermissive,
		},
		{
			name:        "ISC License",
			content:     "Permission to use, copy, modify, and/or distribute this software for any purpose with or without fee is hereby granted...",
			wantLicense: "ISC",
			wantCat:     LicensePermissive,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lic, cat := DetectLicense(tc.content)
			if lic != tc.wantLicense || cat != tc.wantCat {
				t.Errorf("DetectLicense() = (%q, %q), want (%q, %q)", lic, cat, tc.wantLicense, tc.wantCat)
			}
		})
	}
}

func TestDetectLicenseCopyleft(t *testing.T) {
	cases := []struct {
		name        string
		content     string
		wantLicense string
	}{
		{
			name:        "GNU General Public License",
			content:     "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007...",
			wantLicense: "GPL",
		},
		{
			name:        "GNU Affero General Public License",
			content:     "GNU AFFERO GENERAL PUBLIC LICENSE\nVersion 3, 19 November 2007...",
			wantLicense: "AGPL",
		},
		{
			name:        "GNU Lesser General Public License",
			content:     "GNU LESSER GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007...",
			wantLicense: "LGPL",
		},
		{
			name:        "Server Side Public License",
			content:     "Server Side Public License\nVERSION 1, OCTOBER 16, 2018...",
			wantLicense: "SSPL",
		},
		{
			name:        "Mozilla Public License",
			content:     "Mozilla Public License Version 2.0\n==================================",
			wantLicense: "MPL",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lic, cat := DetectLicense(tc.content)
			if lic != tc.wantLicense || cat != LicenseCopyleft {
				t.Errorf("DetectLicense() = (%q, %q), want (%q, %q)", lic, cat, tc.wantLicense, LicenseCopyleft)
			}
		})
	}
}

func TestAuditLicensesCurrentRepo(t *testing.T) {
	report, err := AuditLicenses(".")
	if err != nil {
		t.Fatalf("AuditLicenses failed: %v", err)
	}

	if !report.Clean {
		t.Fatalf("expected clean report for sila dependencies, got violations: %v", report.Violations)
	}
	if report.TotalScanned == 0 {
		t.Errorf("expected scanned modules > 0, got 0")
	}

	// Verify all dependencies are permissive
	for _, m := range report.Modules {
		if m.Category != LicensePermissive {
			t.Errorf("module %s has non-permissive license: %s (%s)", m.Path, m.License, m.Category)
		}
	}
}

func TestAuditLicensesDetectsCopyleftViolation(t *testing.T) {
	tmp := t.TempDir()
	goModContent := `module example.com/testapp

go 1.27

require (
	example.com/bad/copyleft v1.0.0
)
`
	if err := os.WriteFile(filepath.Join(tmp, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create fake module dir simulating cached module
	badModDir := filepath.Join(tmp, "bad-mod")
	if err := os.Mkdir(badModDir, 0755); err != nil {
		t.Fatal(err)
	}
	gplLicense := "GNU GENERAL PUBLIC LICENSE\nVersion 3, 29 June 2007\n\nCopyright (C) 2007 Free Software Foundation, Inc."
	if err := os.WriteFile(filepath.Join(badModDir, "LICENSE"), []byte(gplLicense), 0644); err != nil {
		t.Fatal(err)
	}

	info := evaluateModuleLicense(goModuleJSON{
		Path:    "example.com/bad/copyleft",
		Version: "v1.0.0",
		Dir:     badModDir,
	})

	if info.Category != LicenseCopyleft {
		t.Errorf("expected copyleft classification for simulated module, got %s", info.Category)
	}
	if info.License != "GPL" {
		t.Errorf("expected GPL license, got %s", info.License)
	}

	report := &LicenseAuditReport{
		Clean:        true,
		TotalScanned: 1,
		Modules:      []ModuleLicenseInfo{info},
	}
	if info.Category == LicenseCopyleft {
		report.Clean = false
		report.Violations = append(report.Violations, "unauthorized copyleft license")
	}

	if report.Clean {
		t.Errorf("expected report to be flagged as unclean")
	}
	if len(report.Violations) == 0 || !strings.Contains(report.Violations[0], "unauthorized copyleft") {
		t.Errorf("expected copyleft violation message, got %v", report.Violations)
	}
}
