package ccli

import "strings"

// BuildInventoryFromCatalog converts a validated Catalog into a runtime Inventory.
func BuildInventoryFromCatalog(cat *Catalog) *Inventory {
	if cat == nil {
		return &Inventory{FlagCatalog: SilaFlagCatalog()}
	}
	prods := make([]*Production, 0, len(cat.Productions))
	for _, cp := range cat.Productions {
		slotMap := make(map[string]Slot, len(cp.Slots))
		for _, s := range cp.Slots {
			sCopy := s
			if sCopy.Target {
				sCopy.IsTarget = true
			}
			slotMap[s.Name] = sCopy
		}

		var primary []ProductionElement
		var inversions [][]ProductionElement
		for idx, phrase := range cp.Phrases {
			elems := toProductionElements(phrase, slotMap)
			if idx == 0 {
				primary = elems
			} else {
				inversions = append(inversions, elems)
			}
		}

		fm := make(map[string]string, len(cp.FlagMapping))
		for k, v := range cp.FlagMapping {
			fm[k] = v
		}

		prods = append(prods, &Production{
			ID:                    cp.ID,
			ActionID:              cp.ActionID,
			Domain:                cp.Domain,
			Docs:                  cp.Docs,
			Tier:                  cp.Tier,
			Directional:           cp.Directional || cp.TargetPolicy == "directional",
			Commutative:           cp.Commutative,
			Elements:              primary,
			WhitelistedInversions: inversions,
			CanonicalBase:         append([]string(nil), cp.CanonicalBase...),
			FlagMapping:           fm,
			ArgsOrder:             append([]string(nil), cp.ArgsOrder...),
			StaticFlags:           append([]string(nil), cp.StaticFlags...),
			Slots:                 append([]Slot(nil), cp.Slots...),
			TargetPolicy:          cp.TargetPolicy,
			TargetScope:           cp.TargetScope,
			PlanRow:               cp.PlanRow,
			DryRunPolicy:          cp.DryRunPolicy,
			DryRunAliases:         append([]string(nil), cp.DryRunAliases...),
			ConfirmationPolicy:    cp.ConfirmationPolicy,
			Precedence:            cp.Precedence,
			PrecedenceWitness:     cp.PrecedenceWitness,
			SpecificityWitness:    cp.SpecificityWitness,
			PreserveExit:          cp.PreserveExit,
			VariantOf:             cp.VariantOf,
			Source:                cp.Source,
			SourceCitation:        cp.SourceCitation,
			Correction:            cp.Correction,
			Examples:              cp.Examples,
			Aliases:               append([]string(nil), cp.Aliases...),
			Deprecated:            cp.Deprecated,
			Description:           cp.Description,
		})
	}
	return &Inventory{
		Productions: prods,
		FlagCatalog: SilaFlagCatalog(),
		Catalog:     cloneCatalog(cat),
	}
}

func toProductionElements(phrase []CatalogElement, slots map[string]Slot) []ProductionElement {
	out := make([]ProductionElement, 0, len(phrase))
	for _, ce := range phrase {
		switch strings.ToLower(strings.TrimSpace(ce.Kind)) {
		case "keyword":
			out = append(out, ProductionElement{
				Kind:     ElementKeyword,
				Value:    ce.Value,
				Optional: ce.Optional,
			})
		case "glue":
			out = append(out, ProductionElement{
				Kind:     ElementGlue,
				Value:    ce.Value,
				Optional: ce.Optional,
			})
		case "target":
			s := slots[ce.Slot]
			s.Name = ce.Slot
			s.IsTarget = true
			s.Target = true
			if ce.Greedy {
				s.Greedy = true
			}
			if ce.Optional || s.EffectiveArity() == SlotOptional {
				ce.Optional = true
			}
			out = append(out, ProductionElement{
				Kind:     ElementTarget,
				Optional: ce.Optional,
				Slot:     &s,
			})
		case "slot":
			s := slots[ce.Slot]
			s.Name = ce.Slot
			if ce.Greedy {
				s.Greedy = true
			}
			if ce.Optional || s.EffectiveArity() == SlotOptional {
				ce.Optional = true
			}
			out = append(out, ProductionElement{
				Kind:     ElementSlot,
				Optional: ce.Optional,
				Slot:     &s,
			})
		}
	}
	return out
}

// NewStandardInventory builds and returns the normative standard production inventory
// defined by the Conversational CLI Specification and EBNF grammar.
func NewStandardInventory() *Inventory {
	catalog := FlagCatalog{
		"--verbose": {Arity: 0, Type: "bool", Description: "Enable verbose output"},
		"--help":    {Arity: 0, Type: "bool", Description: "Display usage information"},
		"--dry-run": {Arity: 0, Type: "bool", Description: "Simulate command without mutations"},
		"--yes":     {Arity: 0, Type: "bool", Description: "Bypass confirmation prompt"},
		"--force":   {Arity: 0, Type: "bool", Description: "Force execution"},
		"--lines":   {Arity: 1, Type: "int", Description: "Number of lines"},
		"--env":     {Arity: 1, Type: "string", Description: "Target environment override"},
		"-v":        {Arity: 0, Type: "bool", Description: "Verbose shorthand"},
		"-y":        {Arity: 0, Type: "bool", Description: "Yes shorthand"},
		"-f":        {Arity: 0, Type: "bool", Description: "Force shorthand"},
	}

	prods := []*Production{
		// 1. cluster.status: show status [of] cluster <cluster_name>
		{
			ID:          "prod_cluster_status",
			ActionID:    "cluster.status",
			Tier:        TierQuery,
			Description: "Show status of a cluster",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "show"},
				{Kind: ElementKeyword, Value: "status"},
				{Kind: ElementGlue, Value: "of"},
				{Kind: ElementKeyword, Value: "cluster"},
				{Kind: ElementTarget, Slot: &Slot{Name: "cluster_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			WhitelistedInversions: [][]ProductionElement{
				{
					{Kind: ElementKeyword, Value: "get"},
					{Kind: ElementKeyword, Value: "status"},
					{Kind: ElementGlue, Value: "of"},
					{Kind: ElementKeyword, Value: "cluster"},
					{Kind: ElementTarget, Slot: &Slot{Name: "cluster_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				},
			},
			CanonicalBase: []string{"cluster", "status"},
			FlagMapping:   map[string]string{"cluster_name": "--target"},
			ArgsOrder:     []string{"cluster_name"},
		},

		// 2. metrics.show: show metrics
		{
			ID:          "prod_metrics_show",
			ActionID:    "metrics.show",
			Tier:        TierQuery,
			Description: "Show metrics with target defaulting",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "show"},
				{Kind: ElementKeyword, Value: "metrics"},
			},
			CanonicalBase: []string{"metrics", "show"},
			FlagMapping:   map[string]string{"target": "--target"},
			ArgsOrder:     []string{"target"},
		},

		// 3. status.show: show status
		{
			ID:          "prod_status_show",
			ActionID:    "status.show",
			Tier:        TierQuery,
			Description: "Show status with target defaulting",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "show"},
				{Kind: ElementKeyword, Value: "status"},
			},
			CanonicalBase: []string{"status", "show"},
			FlagMapping:   map[string]string{"target": "--target"},
			ArgsOrder:     []string{"target"},
		},

		// 4. services.list: list services as <format>
		{
			ID:          "prod_services_list",
			ActionID:    "services.list",
			Tier:        TierQuery,
			Description: "List services with format clause",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "list"},
				{Kind: ElementKeyword, Value: "services"},
				{Kind: ElementGlue, Value: "as"},
				{Kind: ElementSlot, Slot: &Slot{Name: "format", Type: SlotTypeOperand, Required: true}},
			},
			CanonicalBase: []string{"services", "list"},
			FlagMapping:   map[string]string{"format": "--format"},
			ArgsOrder:     []string{"format"},
		},

		// 5. service.describe: describe service <service_name>
		{
			ID:          "prod_service_describe",
			ActionID:    "service.describe",
			Tier:        TierQuery,
			Description: "Describe a service by name",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "describe"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"service", "describe"},
			FlagMapping:   map[string]string{"service_name": "--name"},
			ArgsOrder:     []string{"service_name"},
		},

		// 6. logs.get: get logs for service <service_name>
		{
			ID:          "prod_logs_get",
			ActionID:    "logs.get",
			Tier:        TierQuery,
			Description: "Get logs for a service",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "get"},
				{Kind: ElementKeyword, Value: "logs"},
				{Kind: ElementGlue, Value: "for"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"logs", "get"},
			FlagMapping:   map[string]string{"service_name": "--service"},
			ArgsOrder:     []string{"service_name"},
		},

		// 7. node.health: check health of node <node_name>
		{
			ID:          "prod_node_health",
			ActionID:    "node.health",
			Tier:        TierQuery,
			Description: "Check node health",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "check"},
				{Kind: ElementKeyword, Value: "health"},
				{Kind: ElementGlue, Value: "of"},
				{Kind: ElementKeyword, Value: "node"},
				{Kind: ElementTarget, Slot: &Slot{Name: "node_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"node", "health"},
			FlagMapping:   map[string]string{"node_name": "--target"},
			ArgsOrder:     []string{"node_name"},
		},

		// 8. config.show: show config
		{
			ID:          "prod_config_show",
			ActionID:    "config.show",
			Tier:        TierQuery,
			Description: "Show configuration",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "show"},
				{Kind: ElementKeyword, Value: "config"},
			},
			CanonicalBase: []string{"config", "show"},
			FlagMapping:   map[string]string{},
			ArgsOrder:     []string{},
		},

		// 9. user.create: create user <user_name> with role <role_name>
		{
			ID:          "prod_user_create",
			ActionID:    "user.create",
			Tier:        TierMutation,
			Description: "Create a user with specified role",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "create"},
				{Kind: ElementKeyword, Value: "user"},
				{Kind: ElementTarget, Slot: &Slot{Name: "user_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "with"},
				{Kind: ElementKeyword, Value: "role"},
				{Kind: ElementSlot, Slot: &Slot{Name: "role_name", Type: SlotTypeOperand, Required: true}},
			},
			CanonicalBase: []string{"user", "create"},
			FlagMapping:   map[string]string{"user_name": "--name", "role_name": "--role"},
			ArgsOrder:     []string{"user_name", "role_name"},
		},

		// 10. service.deploy: deploy service <service_name> to <environment>
		// Supports commutative: deploy to <environment> service <service_name>
		{
			ID:          "prod_service_deploy",
			ActionID:    "service.deploy",
			Tier:        TierMutation,
			Commutative: true,
			Description: "Deploy a service to target environment",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "deploy"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "to"},
				{Kind: ElementSlot, Slot: &Slot{Name: "environment", Type: SlotTypeOperand, Required: true}},
			},
			WhitelistedInversions: [][]ProductionElement{
				{
					{Kind: ElementKeyword, Value: "deploy"},
					{Kind: ElementGlue, Value: "to"},
					{Kind: ElementSlot, Slot: &Slot{Name: "environment", Type: SlotTypeOperand, Required: true}},
					{Kind: ElementKeyword, Value: "service"},
					{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				},
			},
			CanonicalBase: []string{"service", "deploy"},
			FlagMapping:   map[string]string{"service_name": "--name", "environment": "--env"},
			ArgsOrder:     []string{"service_name", "environment"},
		},

		// 11. service.scale: scale service <service_name> to <replicas>
		{
			ID:          "prod_service_scale",
			ActionID:    "service.scale",
			Tier:        TierMutation,
			Description: "Scale service replicas",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "scale"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				{Kind: ElementGlue, Value: "to"},
				{Kind: ElementSlot, Slot: &Slot{Name: "replicas", Type: SlotTypeInt, Required: true}},
			},
			CanonicalBase: []string{"service", "scale"},
			FlagMapping:   map[string]string{"service_name": "--name", "replicas": "--replicas"},
			ArgsOrder:     []string{"service_name", "replicas"},
		},

		// 12. service.tag: set tag <tag_value> on service <service_name>
		{
			ID:          "prod_service_tag",
			ActionID:    "service.tag",
			Tier:        TierMutation,
			Description: "Set a tag on a service",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "set"},
				{Kind: ElementKeyword, Value: "tag"},
				{Kind: ElementSlot, Slot: &Slot{Name: "tag_value", Type: SlotTypeOperand, Required: true}},
				{Kind: ElementGlue, Value: "on"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"service", "tag"},
			FlagMapping:   map[string]string{"service_name": "--name", "tag_value": "--tag"},
			ArgsOrder:     []string{"service_name", "tag_value"},
		},

		// 13. service.restart: restart service <service_name>
		{
			ID:          "prod_service_restart",
			ActionID:    "service.restart",
			Tier:        TierMutation,
			Description: "Restart a service",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "restart"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"service", "restart"},
			FlagMapping:   map[string]string{"service_name": "--name"},
			ArgsOrder:     []string{"service_name"},
		},

		// 14. bucket.copy: copy bucket from <source> to <destination> (Directional guard)
		{
			ID:          "prod_bucket_copy",
			ActionID:    "bucket.copy",
			Tier:        TierMutation,
			Directional: true,
			Description: "Copy storage bucket from source to destination",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "copy"},
				{Kind: ElementKeyword, Value: "bucket"},
				{Kind: ElementGlue, Value: "from"},
				{Kind: ElementSlot, Slot: &Slot{Name: "source", Type: SlotTypeOperand, Required: true}},
				{Kind: ElementGlue, Value: "to"},
				{Kind: ElementSlot, Slot: &Slot{Name: "destination", Type: SlotTypeOperand, Required: true}},
			},
			CanonicalBase: []string{"bucket", "copy"},
			FlagMapping:   map[string]string{"source": "--from", "destination": "--to"},
			ArgsOrder:     []string{"source", "destination"},
		},

		// 15. service.description: update description <desc_text> for service <service_name>
		{
			ID:          "prod_service_description",
			ActionID:    "service.description",
			Tier:        TierMutation,
			Description: "Update description of a service",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "update"},
				{Kind: ElementKeyword, Value: "description"},
				{Kind: ElementSlot, Slot: &Slot{Name: "desc_text", Type: SlotTypeOperand, Required: true}},
				{Kind: ElementGlue, Value: "for"},
				{Kind: ElementKeyword, Value: "service"},
				{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"service", "description"},
			FlagMapping:   map[string]string{"service_name": "--name", "desc_text": "--text"},
			ArgsOrder:     []string{"service_name", "desc_text"},
		},

		// 16. database.purge: purge database <database_name>
		{
			ID:          "prod_database_purge",
			ActionID:    "database.purge",
			Tier:        TierDestructive,
			Description: "Purge a database",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "purge"},
				{Kind: ElementKeyword, Value: "database"},
				{Kind: ElementTarget, Slot: &Slot{Name: "database_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"database", "purge"},
			FlagMapping:   map[string]string{"database_name": "--name"},
			ArgsOrder:     []string{"database_name"},
		},

		// 17. cluster.delete: delete cluster <cluster_name> [with force]
		{
			ID:          "prod_cluster_delete",
			ActionID:    "cluster.delete",
			Tier:        TierDestructive,
			Description: "Delete a cluster",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "delete"},
				{Kind: ElementKeyword, Value: "cluster"},
				{Kind: ElementTarget, Slot: &Slot{Name: "cluster_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			WhitelistedInversions: [][]ProductionElement{
				{
					{Kind: ElementKeyword, Value: "delete"},
					{Kind: ElementKeyword, Value: "cluster"},
					{Kind: ElementTarget, Slot: &Slot{Name: "cluster_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
					{Kind: ElementGlue, Value: "with"},
					{Kind: ElementKeyword, Value: "force"},
				},
			},
			CanonicalBase: []string{"cluster", "delete"},
			FlagMapping:   map[string]string{"cluster_name": "--name"},
			ArgsOrder:     []string{"cluster_name"},
		},

		// 18. table.drop: drop table <table_name>
		{
			ID:          "prod_table_drop",
			ActionID:    "table.drop",
			Tier:        TierDestructive,
			Description: "Drop a database table",
			Elements: []ProductionElement{
				{Kind: ElementKeyword, Value: "drop"},
				{Kind: ElementKeyword, Value: "table"},
				{Kind: ElementTarget, Slot: &Slot{Name: "table_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
			},
			CanonicalBase: []string{"table", "drop"},
			FlagMapping:   map[string]string{"table_name": "--name"},
			ArgsOrder:     []string{"table_name"},
		},
	}

	return &Inventory{
		Productions: prods,
		FlagCatalog: catalog,
	}
}

// BuildCollisionInventory constructs an ambiguous inventory matching a specific collision witness.
func BuildCollisionInventory(collisionID string) *Inventory {
	inv := &Inventory{
		FlagCatalog: FlagCatalog{},
		Productions: []*Production{},
	}

	switch collisionID {
	case "collision_exact_skeleton_status_service":
		// Conflicting: cluster.status vs service.status on "show status production"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:       "prod_cluster_status_skeleton",
				ActionID: "cluster.status",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "show"},
					{Kind: ElementKeyword, Value: "status"},
					{Kind: ElementTarget, Slot: &Slot{Name: "target", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				},
			},
			&Production{
				ID:       "prod_service_status_skeleton",
				ActionID: "service.status",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "show"},
					{Kind: ElementKeyword, Value: "status"},
					{Kind: ElementTarget, Slot: &Slot{Name: "target", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				},
			},
		)

	case "collision_keyword_slot_overlap_logs":
		// Conflicting: logs.view vs resource.get on "get logs"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:       "prod_logs_view",
				ActionID: "logs.view",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "get"},
					{Kind: ElementKeyword, Value: "logs"},
				},
			},
			&Production{
				ID:       "prod_resource_get",
				ActionID: "resource.get",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "get"},
					{Kind: ElementSlot, Slot: &Slot{Name: "resource", Type: SlotTypeOperand, Required: true}},
				},
			},
		)

	case "collision_optional_segment_collapse_deploy":
		// Conflicting: deploy.full vs deploy.quick on "deploy web-api"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:       "prod_deploy_full",
				ActionID: "deploy.full",
				Tier:     TierMutation,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "deploy"},
					{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
					{Kind: ElementGlue, Value: "to", Optional: true},
					{Kind: ElementSlot, Slot: &Slot{Name: "env", Type: SlotTypeOperand, Required: false}, Optional: true},
				},
			},
			&Production{
				ID:       "prod_deploy_quick",
				ActionID: "deploy.quick",
				Tier:     TierMutation,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "deploy"},
					{Kind: ElementTarget, Slot: &Slot{Name: "service_name", Type: SlotTypeTarget, Required: true, IsTarget: true}},
				},
			},
		)

	case "collision_commutative_overlap_copy":
		// Conflicting: bucket.copy vs file.transfer on "copy bucket-a bucket-b"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:          "prod_bucket_copy_comm",
				ActionID:    "bucket.copy",
				Tier:        TierMutation,
				Commutative: true,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "copy"},
					{Kind: ElementSlot, Slot: &Slot{Name: "source", Type: SlotTypeOperand, Required: true}},
					{Kind: ElementSlot, Slot: &Slot{Name: "destination", Type: SlotTypeOperand, Required: true}},
				},
			},
			&Production{
				ID:          "prod_file_transfer_comm",
				ActionID:    "file.transfer",
				Tier:        TierMutation,
				Commutative: true,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "copy"},
					{Kind: ElementSlot, Slot: &Slot{Name: "source", Type: SlotTypeOperand, Required: true}},
					{Kind: ElementSlot, Slot: &Slot{Name: "destination", Type: SlotTypeOperand, Required: true}},
				},
			},
		)

	case "collision_keyword_slot_inspect_metrics":
		// Conflicting: metrics.inspect vs resource.inspect on "inspect metrics"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:       "prod_metrics_inspect",
				ActionID: "metrics.inspect",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "inspect"},
					{Kind: ElementKeyword, Value: "metrics"},
				},
			},
			&Production{
				ID:       "prod_resource_inspect",
				ActionID: "resource.inspect",
				Tier:     TierQuery,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "inspect"},
					{Kind: ElementSlot, Slot: &Slot{Name: "target", Type: SlotTypeOperand, Required: true}},
				},
			},
		)

	case "collision_optional_collapse_restart":
		// Conflicting: service.restart_all vs service.restart_one on "restart service"
		inv.Productions = append(inv.Productions,
			&Production{
				ID:       "prod_restart_all",
				ActionID: "service.restart_all",
				Tier:     TierMutation,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "restart"},
					{Kind: ElementKeyword, Value: "service"},
				},
			},
			&Production{
				ID:       "prod_restart_one",
				ActionID: "service.restart_one",
				Tier:     TierMutation,
				Elements: []ProductionElement{
					{Kind: ElementKeyword, Value: "restart"},
					{Kind: ElementKeyword, Value: "service"},
					{Kind: ElementTarget, Slot: &Slot{Name: "target", Type: SlotTypeTarget, Required: false}, Optional: true},
				},
			},
		)
	}

	return inv
}
