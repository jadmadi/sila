package mcp

// graphToolDefinitions returns the read-only code + memory graph tools.
func graphToolDefinitions() []Tool {
	return []Tool{
		{
			Name:        "query_graph",
			Description: "Rank and expand code + memory graph nodes for a question; returns a bounded subgraph with provenance scores.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"question":     {Type: "string", Description: "Question, symbol, or free-text terms to rank graph nodes"},
					"project_dir":  {Type: "string", Description: "Repository root (defaults to cwd)"},
					"max_depth":    {Type: "integer", Description: "Maximum expansion depth (default 2)"},
					"token_budget": {Type: "integer", Description: "Deprecated alias for max_nodes"},
					"max_nodes":    {Type: "integer", Description: "Maximum selected nodes (default 200)"},
					"max_bytes":    {Type: "integer", Description: "Maximum result bytes (default 65536)"},
					"files":        {Type: "array", Description: "Optional repository files to include as slice seeds"},
				},
			},
		},
		{
			Name:        "shortest_path",
			Description: "Find the most reliable confidence-weighted path between two graph nodes; ambiguous bare names fail with candidates.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"source":      {Type: "string", Description: "Source node ID, qualified symbol, or unique label"},
					"target":      {Type: "string", Description: "Target node ID, qualified symbol, or unique label"},
					"project_dir": {Type: "string", Description: "Repository root (defaults to cwd)"},
					"max_hops":    {Type: "integer", Description: "Maximum path hops (default 6)"},
					"code_only":   {Type: "boolean", Description: "Follow only code relations and skip memory nodes"},
				},
				Required: []string{"source", "target"},
			},
		},
		{
			Name:        "graph_report",
			Description: "Audit the code + memory graph: communities, modularity, god nodes, surprising connections, and suggested questions.",
			InputSchema: InputSchema{
				Type: "object",
				Properties: map[string]PropertySchema{
					"project_dir": {Type: "string", Description: "Repository root (defaults to cwd)"},
					"rebuild":     {Type: "boolean", Description: "Rebuild the graph in memory before reporting (no file write)"},
					"top":         {Type: "integer", Description: "Communities, god nodes, and surprises to keep (default 10)"},
					"full":        {Type: "boolean", Description: "Return the complete report instead of the bounded summary"},
				},
			},
		},
	}
}
