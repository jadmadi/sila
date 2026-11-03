package mcp

// DefaultUpstreamToolDefinitions returns static tool definitions for the 5 default upstream servers.
func DefaultUpstreamToolDefinitions() []Tool {
	return []Tool{
		// 1. UI Craft tools (Prefix: uicraft_)
		{
			Name:        "uicraft_route_task",
			Description: "[Upstream: ui-craft] Route a UI design task or request to the appropriate UI Craft recipe, archetype, or pattern.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"prompt":  {Type: "string", Description: "User prompt, feature description, or UI objective"},
					"project": {Type: "string", Description: "Optional target project or surface context"},
				},
				Required: []string{"prompt"},
			},
		},
		{
			Name:        "uicraft_check_anti_slop",
			Description: "[Upstream: ui-craft] Scan frontend code or file path for generic AI slop, placeholder values, and clichéd styling.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string", Description: "Inline JSX/HTML/CSS code string to scan"},
					"path": {Type: "string", Description: "File path or directory to scan"},
				},
			},
		},
		{
			Name:        "uicraft_tokens_lint",
			Description: "[Upstream: ui-craft] Lint CSS/Tailwind design tokens for spacing, color scale, font ramp, and consistency.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string", Description: "Inline component code or CSS to lint"},
					"path": {Type: "string", Description: "File path or directory to lint"},
				},
			},
		},
		{
			Name:        "uicraft_acceptance_bar",
			Description: "[Upstream: ui-craft] Evaluate UI code against acceptance bar criteria for specific archetypes (dashboard, landing, auth, generic).",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"surface": {Type: "string", Description: "Archetype name (dashboard, landing, auth, generic)"},
					"code":    {Type: "string", Description: "Inline code to evaluate"},
					"path":    {Type: "string", Description: "Target file path to evaluate"},
				},
			},
		},
		{
			Name:        "uicraft_ux_coverage",
			Description: "[Upstream: ui-craft] Verify UX state coverage: empty states, loading states, error states, and responsive breakpoints.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"surface": {Type: "string", Description: "Archetype name (dashboard, landing, auth, generic)"},
					"code":    {Type: "string", Description: "Inline code to evaluate"},
					"path":    {Type: "string", Description: "Target file path to evaluate"},
				},
			},
		},
		{
			Name:        "uicraft_score_ui",
			Description: "[Upstream: ui-craft] Compute composite UI Craft Score (0-100) across typography, spacing, contrast, motion, and states.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string", Description: "Inline code to score"},
					"path": {Type: "string", Description: "File path to score"},
				},
			},
		},
		{
			Name:        "uicraft_fold_candidates",
			Description: "[Upstream: ui-craft] Identify above-the-fold content candidates and critical render path components.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string", Description: "Inline code or markup to inspect"},
					"path": {Type: "string", Description: "File path to inspect"},
				},
			},
		},
		{
			Name:        "uicraft_check_fold",
			Description: "[Upstream: ui-craft] Audit a deployed URL or preview server for above-the-fold layout shifts and visual hierarchy.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"url": {Type: "string", Description: "Live HTTP/HTTPS URL to check"},
				},
				Required: []string{"url"},
			},
		},

		// 2. Drizzle ORM Docs tools (Prefix: drizzle_)
		{
			Name:        "drizzle_list_topics",
			Description: "[Upstream: drizzle-docs] Browse Drizzle ORM documentation catalogue across all dialects and sections.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"dialect": {Type: "string", Description: "SQL dialect filter (pg, mysql, sqlite, all)"},
					"section": {Type: "string", Description: "Section filter (e.g. quickstart, queries, migrations)"},
					"limit":   {Type: "integer", Description: "Maximum topics to return (default 60)"},
					"offset":  {Type: "integer", Description: "Topics offset to skip"},
				},
			},
		},
		{
			Name:        "drizzle_search_docs",
			Description: "[Upstream: drizzle-docs] Search 450+ pages of Drizzle ORM documentation with fuzzy matching and full-text index.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":   {Type: "string", Description: "Search terms or phrase (e.g. 'batch api', 'd1 migrations')"},
					"depth":   {Type: "string", Description: "Search depth: 'index' (default) or 'full'"},
					"dialect": {Type: "string", Description: "SQL dialect filter (pg, mysql, sqlite)"},
					"section": {Type: "string", Description: "Documentation section filter"},
					"limit":   {Type: "integer", Description: "Maximum results to return (default 10)"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "drizzle_fetch_page",
			Description: "[Upstream: drizzle-docs] Fetch a full Drizzle documentation page as clean Markdown with navigation stripped.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"slug":      {Type: "string", Description: "Documentation slug (e.g. 'docs/overview', 'docs/migrations')"},
					"format":    {Type: "string", Description: "Format: markdown (default), json, plaintext"},
					"maxLength": {Type: "integer", Description: "Maximum character length limit"},
				},
				Required: []string{"slug"},
			},
		},

		// 3. Cloudflare tools (Prefix: cf_)
		{
			Name:        "cf_search",
			Description: "[Upstream: cloudflare] Search Cloudflare API endpoints, documentation, and schema definitions in Code Mode.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "Search query across Cloudflare API endpoints and docs"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "cf_execute",
			Description: "[Upstream: cloudflare] Execute dynamic JavaScript code against Cloudflare API endpoints in Code Mode.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"code": {Type: "string", Description: "JavaScript code string to execute against Cloudflare API"},
				},
				Required: []string{"code"},
			},
		},

		// 4. gopls tools (Prefix: gopls_)
		{
			Name:        "gopls_diagnostics",
			Description: "[Upstream: gopls] Retrieve compiler errors, typecheck failures, and vet diagnostics from gopls language server.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"path":    {Type: "string", Description: "Target Go file or package directory"},
					"project": {Type: "string", Description: "Optional project ID or directory"},
				},
			},
		},
		{
			Name:        "gopls_workspace",
			Description: "[Upstream: gopls] Inspect Go workspace modules, packages, and build environment configuration.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project": {Type: "string", Description: "Optional project ID or directory"},
				},
			},
		},

		// 5. Astro tools (Prefix: astro_)
		{
			Name:        "astro_search_astro_docs",
			Description: "[Upstream: astro] Search official Astro framework documentation.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "Search query across official Astro documentation"},
				},
				Required: []string{"query"},
			},
		},
	}
}

// AlphaXivToolDefinitions returns static tool schemas for alphaxiv when toggled on.
func AlphaXivToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "alphaxiv_discover_papers",
			Description: "[Upstream: alphaxiv] Discovers and ranks candidate papers for a research topic across 2.5M+ arXiv academic papers.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"keywords":   {Type: "array", Description: "Search terms or key phrases"},
					"question":   {Type: "string", Description: "Research question or topic"},
					"difficulty": {Type: "number", Description: "Retrieval effort estimate (1-10)"},
					"prioritize": {Type: "string", Description: "'recency', 'popular', 'historical', or 'default'"},
				},
				Required: []string{"keywords", "question", "difficulty"},
			},
		},
		{
			Name:        "alphaxiv_get_paper_content",
			Description: "[Upstream: alphaxiv] Get the text or structured LLM summary of an arXiv/alphaXiv paper. Auto-cached to local SQLite FTS5 store.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"url":      {Type: "string", Description: "arXiv or alphaXiv URL (e.g. 'https://arxiv.org/abs/2307.12307')"},
					"fullText": {Type: "boolean", Description: "If true, return full extracted text instead of intermediate summary"},
				},
				Required: []string{"url"},
			},
		},
		{
			Name:        "alphaxiv_answer_pdf_queries",
			Description: "[Upstream: alphaxiv] Answer targeted technical queries about a specific paper PDF.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"url":     {Type: "string", Description: "Paper arXiv or alphaXiv URL"},
					"queries": {Type: "array", Description: "List of specific questions to answer from the paper"},
				},
				Required: []string{"url", "queries"},
			},
		},
		{
			Name:        "alphaxiv_find_researchers",
			Description: "[Upstream: alphaxiv] Search academic authors and researchers by topic, institute, or name.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query": {Type: "string", Description: "Researcher name or research field"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "alphaxiv_list_library",
			Description: "[Upstream: alphaxiv] List folders and bookmarked papers in your AlphaXiv library.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]PropertySchema{},
			},
		},
	}
}

// ScienceToolDefinitions returns static tool schemas for ScienceMadeReadable when toggled on.
func ScienceToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "science_search_papers",
			Description: "[Upstream: science] Search 211M+ OpenAlex scientific papers by semantic meaning or exact boolean keywords.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"query":            {Type: "string", Description: "Search query (semantic meaning or boolean syntax)"},
					"limit":            {Type: "integer", Description: "Maximum papers to return (1-100, default 10)"},
					"mode":             {Type: "string", Description: "Search mode: 'auto', 'semantic', or 'keyword'"},
					"year_from":        {Type: "integer", Description: "Earliest publication year"},
					"year_to":          {Type: "integer", Description: "Latest publication year"},
					"publication_type": {Type: "string", Description: "Filter by publication type (e.g. 'article', 'review')"},
					"open_access_only": {Type: "boolean", Description: "Restrict results to open-access papers"},
					"readable_only":    {Type: "boolean", Description: "Restrict results to papers with readable full text"},
					"abstracts":        {Type: "string", Description: "Abstract detail: 'short', 'full', or 'none'"},
				},
				Required: []string{"query"},
			},
		},
		{
			Name:        "science_get_papers",
			Description: "[Upstream: science] Retrieve full structured records and abstracts for up to 20 papers by ID, DOI, or W-number.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"ids": {Type: "array", Description: "Paper identifiers (OpenAlex IDs, DOIs, or W-numbers)"},
				},
				Required: []string{"ids"},
			},
		},
		{
			Name:        "science_find_similar_papers",
			Description: "[Upstream: science] Find papers closest in semantic meaning to a source paper.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":        {Type: "string", Description: "Source paper identifier"},
					"limit":     {Type: "integer", Description: "Maximum similar papers to return (1-50, default 10)"},
					"abstracts": {Type: "string", Description: "Abstract detail: 'short', 'full', or 'none'"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "science_citation_walk",
			Description: "[Upstream: science] Explore the citation graph and bibliographic couplings around a paper.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":             {Type: "string", Description: "Paper identifier to walk citations from"},
					"max_neighbours": {Type: "integer", Description: "Maximum neighbours to expand (default 10)"},
					"limit":          {Type: "integer", Description: "Maximum results to return (default 10)"},
				},
				Required: []string{"id"},
			},
		},
		{
			Name:        "science_map_topic",
			Description: "[Upstream: science] Map a research topic into clusters, seminal works, and reading lists.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"topic": {Type: "string", Description: "Research topic to map"},
					"scope": {Type: "string", Description: "Mapping scope: 'quick' or 'whole_field'"},
					"group": {Type: "integer", Description: "Cluster group to focus on"},
					"page":  {Type: "integer", Description: "Result page"},
				},
				Required: []string{"topic"},
			},
		},
		{
			Name:        "science_read_topic_group",
			Description: "[Upstream: science] Deep-read a clustered topic group with full abstracts and reading lists.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"topic": {Type: "string", Description: "Research topic"},
					"group": {Type: "integer", Description: "Cluster group to read"},
					"part":  {Type: "string", Description: "Section to return: 'lists' or 'papers'"},
					"page":  {Type: "integer", Description: "Result page"},
				},
				Required: []string{"topic", "group"},
			},
		},
		{
			Name:        "science_find_people",
			Description: "[Upstream: science] Search authors, institutions, or journal sources by name.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"name":  {Type: "string", Description: "Author, institution, or source name"},
					"kind":  {Type: "string", Description: "Entity kind: 'author', 'institution', or 'source'"},
					"limit": {Type: "integer", Description: "Maximum results to return (default 10)"},
				},
				Required: []string{"name"},
			},
		},
		{
			Name:        "science_get_profile",
			Description: "[Upstream: science] Retrieve publication history and profiles for authors, institutions, or venues.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"id":            {Type: "string", Description: "Author, institution, or venue identifier"},
					"group":         {Type: "string", Description: "Profile group filter"},
					"list":          {Type: "string", Description: "Profile list filter"},
					"offset":        {Type: "integer", Description: "Result offset"},
					"together_with": {Type: "array", Description: "Co-author or related entity identifiers"},
				},
				Required: []string{"id"},
			},
		},
	}
}

// StitchToolDefinitions returns static tool schemas for StitchMCP when toggled on.
func StitchToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "stitch_generate_screen_from_text",
			Description: "[Upstream: Google Stitch] Generates a new UI screen within a project from a text prompt.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"projectId": {Type: "string", Description: "The Stitch project ID"},
					"prompt":    {Type: "string", Description: "Text prompt describing the desired screen"},
				},
				Required: []string{"projectId", "prompt"},
			},
		},
		{
			Name:        "stitch_generate_variants",
			Description: "[Upstream: Google Stitch] Generates variants of existing UI screens within a project.",
			Category:    ToolCategoryWrite,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"projectId":         {Type: "string", Description: "The Stitch project ID"},
					"selectedScreenIds": {Type: "array", Description: "Screen IDs to generate variants for"},
					"prompt":            {Type: "string", Description: "Instructions for variation"},
				},
				Required: []string{"projectId", "selectedScreenIds", "prompt"},
			},
		},
		{
			Name:        "stitch_get_screen",
			Description: "[Upstream: Google Stitch] Retrieves screen metadata, structure, and design assets.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"projectId": {Type: "string", Description: "Stitch project ID"},
					"screenId":  {Type: "string", Description: "Screen ID to retrieve"},
				},
				Required: []string{"projectId", "screenId"},
			},
		},
		{
			Name:        "stitch_list_screens",
			Description: "[Upstream: Google Stitch] Lists all screens associated with a project.",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"projectId": {Type: "string", Description: "Stitch project ID"},
				},
				Required: []string{"projectId"},
			},
		},
		{
			Name:        "stitch_list_projects",
			Description: "[Upstream: Google Stitch] Lists all Google Stitch projects (namespaced to avoid collision with Sila list_projects).",
			Category:    ToolCategoryRead,
			InputSchema: InputSchema{
				Type:       "object",
				Properties: map[string]PropertySchema{},
			},
		},
	}
}
