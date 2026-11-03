package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jadmadi/sila/internal/store"
)

// PackageDoc represents structured documentation and metadata for an official software package.
type PackageDoc struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
	Version   string `json:"version"`
	Summary   string `json:"summary"`
	DocURL    string `json:"doc_url"`
	RepoURL   string `json:"repo_url,omitempty"`
	Content   string `json:"content"`
	Author    string `json:"author,omitempty"`
	License   string `json:"license,omitempty"`
}

// FetchPackageDoc retrieves official package documentation from the specified registry.
func FetchPackageDoc(name, ecosystem string) (*PackageDoc, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("package name cannot be empty")
	}

	eco := strings.ToLower(strings.TrimSpace(ecosystem))
	if eco == "" || eco == "auto" {
		eco = detectEcosystem(name)
	}

	switch eco {
	case "go", "golang":
		return fetchGoPackageDoc(name)
	case "rust", "cargo", "crates", "crates.io":
		return fetchRustPackageDoc(name)
	case "npm", "node", "javascript", "typescript", "js", "ts":
		return fetchNPMPackageDoc(name)
	case "python", "pypi", "py":
		return fetchPyPIPackageDoc(name)
	default:
		// Attempt in order: Go -> NPM -> Rust -> Python
		if strings.Contains(name, "/") {
			if doc, err := fetchGoPackageDoc(name); err == nil {
				return doc, nil
			}
			if doc, err := fetchNPMPackageDoc(name); err == nil {
				return doc, nil
			}
		}
		if doc, err := fetchNPMPackageDoc(name); err == nil {
			return doc, nil
		}
		if doc, err := fetchRustPackageDoc(name); err == nil {
			return doc, nil
		}
		if doc, err := fetchPyPIPackageDoc(name); err == nil {
			return doc, nil
		}
		return nil, fmt.Errorf("unable to find package %q in Go, NPM, Crates, or PyPI", name)
	}
}

func detectEcosystem(name string) string {
	if strings.Contains(name, "github.com/") || strings.Contains(name, "golang.org/") || strings.Contains(name, "gopkg.in/") {
		return "go"
	}
	if strings.HasPrefix(name, "@") {
		return "npm"
	}
	return "auto"
}

func fetchNPMPackageDoc(name string) (*PackageDoc, error) {
	url := "https://registry.npmjs.org/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("npm registry returned HTTP %d", resp.StatusCode)
	}

	var data struct {
		Name        string `json:"name"`
		Description string `json:"description"`
		Readme      string `json:"readme"`
		Homepage    string `json:"homepage"`
		License     string `json:"license"`
		DistTags    struct {
			Latest string `json:"latest"`
		} `json:"dist-tags"`
		Repository struct {
			URL string `json:"url"`
		} `json:"repository"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	readme := strings.TrimSpace(data.Readme)
	if readme == "" {
		readme = data.Description
	}

	repoURL := data.Repository.URL
	repoURL = strings.TrimPrefix(repoURL, "git+")
	repoURL = strings.TrimSuffix(repoURL, ".git")

	docURL := "https://www.npmjs.com/package/" + data.Name
	if data.Homepage != "" {
		docURL = data.Homepage
	}

	content := fmt.Sprintf("# NPM Package: %s (%s)\n\n**Description**: %s\n**Doc URL**: %s\n**License**: %s\n\n---\n\n%s",
		data.Name, data.DistTags.Latest, data.Description, docURL, data.License, readme)

	return &PackageDoc{
		Name:      data.Name,
		Ecosystem: "npm",
		Version:   data.DistTags.Latest,
		Summary:   data.Description,
		DocURL:    docURL,
		RepoURL:   repoURL,
		Content:   content,
		License:   data.License,
	}, nil
}

func fetchRustPackageDoc(name string) (*PackageDoc, error) {
	url := "https://crates.io/api/v1/crates/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("crates.io returned HTTP %d", resp.StatusCode)
	}

	var data struct {
		Crate struct {
			Name          string `json:"name"`
			MaxVersion    string `json:"max_version"`
			Description   string `json:"description"`
			Documentation string `json:"documentation"`
			Homepage      string `json:"homepage"`
			Repository    string `json:"repository"`
		} `json:"crate"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	docURL := data.Crate.Documentation
	if docURL == "" {
		docURL = "https://docs.rs/" + data.Crate.Name
	}

	content := fmt.Sprintf("# Rust Crate: %s (v%s)\n\n**Description**: %s\n**Docs.rs**: %s\n**Repository**: %s\n\n```toml\n[dependencies]\n%s = \"%s\"\n```\n",
		data.Crate.Name, data.Crate.MaxVersion, data.Crate.Description, docURL, data.Crate.Repository, data.Crate.Name, data.Crate.MaxVersion)

	return &PackageDoc{
		Name:      data.Crate.Name,
		Ecosystem: "rust",
		Version:   data.Crate.MaxVersion,
		Summary:   data.Crate.Description,
		DocURL:    docURL,
		RepoURL:   data.Crate.Repository,
		Content:   content,
	}, nil
}

func fetchPyPIPackageDoc(name string) (*PackageDoc, error) {
	url := fmt.Sprintf("https://pypi.org/pypi/%s/json", name)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "sila/1.0 (+https://github.com/jadmadi/sila)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pypi returned HTTP %d", resp.StatusCode)
	}

	var data struct {
		Info struct {
			Name        string `json:"name"`
			Version     string `json:"version"`
			Summary     string `json:"summary"`
			Description string `json:"description"`
			HomePage    string `json:"home_page"`
			ProjectURL  string `json:"project_url"`
			Author      string `json:"author"`
			License     string `json:"license"`
		} `json:"info"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}

	docURL := "https://pypi.org/project/" + data.Info.Name
	if data.Info.ProjectURL != "" {
		docURL = data.Info.ProjectURL
	}

	desc := data.Info.Description
	if len(desc) > 30000 {
		desc = desc[:30000] + "\n\n...(truncated)..."
	}

	content := fmt.Sprintf("# PyPI Package: %s (%s)\n\n**Summary**: %s\n**Doc URL**: %s\n**Author**: %s\n**License**: %s\n\n---\n\n%s",
		data.Info.Name, data.Info.Version, data.Info.Summary, docURL, data.Info.Author, data.Info.License, desc)

	return &PackageDoc{
		Name:      data.Info.Name,
		Ecosystem: "python",
		Version:   data.Info.Version,
		Summary:   data.Info.Summary,
		DocURL:    docURL,
		RepoURL:   data.Info.HomePage,
		Content:   content,
		Author:    data.Info.Author,
		License:   data.Info.License,
	}, nil
}

func fetchGoPackageDoc(name string) (*PackageDoc, error) {
	pkgURL := "https://pkg.go.dev/" + name
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", pkgURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pkg.go.dev returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return nil, err
	}

	title, md := HTMLToMarkdown(string(body))
	summary := fmt.Sprintf("Go package documentation for %s", name)

	content := fmt.Sprintf("# Go Package: %s\n\n**URL**: %s\n\n---\n\n%s", name, pkgURL, md)

	return &PackageDoc{
		Name:      name,
		Ecosystem: "go",
		Version:   "latest",
		Summary:   summary,
		DocURL:    pkgURL,
		Content:   content,
		Author:    title,
	}, nil
}

// IngestPackageDoc saves the package doc directly into the project's knowledge store.
func IngestPackageDoc(st *store.Store, doc *PackageDoc, projectID string) (*store.Doc, error) {
	if st == nil || doc == nil {
		return nil, errors.New("invalid store or doc")
	}

	target := fmt.Sprintf("pkg:%s/%s", doc.Ecosystem, doc.Name)
	d := store.Doc{
		ID:        store.DocID(projectID, target),
		ProjectID: projectID,
		Title:     fmt.Sprintf("[%s] %s (%s)", strings.ToUpper(doc.Ecosystem), doc.Name, doc.Version),
		URL:       doc.DocURL,
		Content:   doc.Content,
		Format:    "markdown",
		CreatedAt: time.Now().UTC().Format(time.RFC3339),
	}

	if err := st.InsertDoc(d); err != nil {
		return nil, err
	}
	return &d, nil
}
