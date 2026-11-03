package audit

import (
	"fmt"
	"strings"

	"github.com/jadmadi/sila/internal/persona"
)

// SquadPreset represents a named squad template.
type SquadPreset struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	PersonaIDs  []string `json:"persona_ids"`
}

// DefaultPresets contains curated squad templates.
var DefaultPresets = map[string]SquadPreset{
	"ui-ux": {
		Name:        "ui-ux",
		Description: "Design craft, ergonomics, accessibility, and newcomer empathy",
		PersonaIDs: []string{
			"accessibility-sentinel",
			"ui-craftsperson",
			"ux-information-architect",
			"first-time-user-advocate",
		},
	},
	"conversion": {
		Name:        "conversion",
		Description: "High-stakes funnel, copywriting, customer psychology, and sales readiness",
		PersonaIDs: []string{
			"conversion-copywriter",
			"growth-product-strategist",
			"enterprise-sales-investor",
			"negative-churn-risk-customer",
			"positive-champion-customer",
		},
	},
	"technical": {
		Name:        "technical",
		Description: "Deep engineering, Core Web Vitals, security posture, keyboard navigation, and telemetry",
		PersonaIDs: []string{
			"senior-fullstack-engineer",
			"performance-sentinel",
			"security-compliance-sentry",
			"keyboard-power-maximalist",
			"telemetry-data-forensic",
			"community-open-source-steward",
		},
	},
	"executive": {
		Name:        "executive",
		Description: "Strategic devil's advocacy, unconstrained vision, ethics, and legal compliance",
		PersonaIDs: []string{
			"devils-advocate-contrarian",
			"visionary-dreamer",
			"epistemic-philosopher",
			"legal-compliance-counsel",
			"competitor-recon-strategist",
		},
	},
	"arabic-waqf": {
		Name:        "arabic-waqf",
		Description: "Specialized for Islamic cultural integrity, classical Arabic copy, and Waqf dignity",
		PersonaIDs: []string{
			"arabic-waqf-craftsperson",
			"ui-craftsperson",
			"ux-information-architect",
			"accessibility-sentinel",
		},
	},
	"qa-testing": {
		Name:        "qa-testing",
		Description: "Automated test coverage, edge cases, mobile touch gestures, and security boundaries",
		PersonaIDs: []string{
			"qa-automation-lead",
			"accessibility-sentinel",
			"security-compliance-sentry",
			"performance-sentinel",
			"mobile-native-ergonomist",
		},
	},
	"compliance": {
		Name:        "compliance",
		Description: "Regulatory compliance, healthcare HIPAA privacy, legal liability, and ethical guardrails",
		PersonaIDs: []string{
			"legal-compliance-counsel",
			"hipaa-healthcare-compliance",
			"security-compliance-sentry",
			"epistemic-philosopher",
		},
	},
	"agent-ready": {
		Name:        "agent-ready",
		Description: "AI agent readiness, protocol discovery (MCP, A2A, WebMCP, ARD), markdown negotiation, token efficiency, and agentic commerce",
		PersonaIDs: []string{
			"agent-readiness-architect",
			"content-negotiation-token-economist",
			"machine-identity-auth-counsel",
			"agentic-commerce-auditor",
			"seo-discoverability-auditor",
		},
	},
}

// RecommendSquad analyzes target URL and descriptive context to recommend a squad.
func RecommendSquad(allPersonas []persona.Persona, targetURL, contextText string) []persona.Persona {
	idMap := make(map[string]persona.Persona, len(allPersonas))
	for _, p := range allPersonas {
		idMap[p.ID] = p
	}

	selectedIDs := make(map[string]bool)
	lowerURL := strings.ToLower(targetURL)
	lowerCtx := strings.ToLower(contextText)
	combined := lowerURL + " " + lowerCtx

	// Core baseline always present in any web audit
	for _, coreID := range []string{"accessibility-sentinel", "ui-craftsperson", "ux-information-architect"} {
		if _, ok := idMap[coreID]; ok {
			selectedIDs[coreID] = true
		}
	}

	// Senior Programmer / Core Developer detection
	if strings.Contains(combined, "programmer") || strings.Contains(combined, "developer") ||
		strings.Contains(combined, "fullstack") || strings.Contains(combined, "backend") ||
		strings.Contains(combined, "architecture") || strings.Contains(combined, "refactor") {
		selectedIDs["senior-fullstack-engineer"] = true
	}

	// QA & Test Automation detection
	if strings.Contains(combined, "qa") || strings.Contains(combined, "test") ||
		strings.Contains(combined, "automation") || strings.Contains(combined, "regression") ||
		strings.Contains(combined, "e2e") {
		selectedIDs["qa-automation-lead"] = true
	}

	// Mobile Native & Touch Gestures
	if strings.Contains(combined, "mobile") || strings.Contains(combined, "ios") ||
		strings.Contains(combined, "android") || strings.Contains(combined, "touch") ||
		strings.Contains(combined, "flutter") || strings.Contains(combined, "react native") {
		selectedIDs["mobile-native-ergonomist"] = true
	}

	// Arabic / Waqf detection
	if strings.Contains(combined, "arabic") || strings.Contains(combined, "waqf") ||
		strings.Contains(combined, "manara") || strings.Contains(combined, "islamic") ||
		strings.Contains(combined, ".ar") || strings.Contains(combined, "fusha") ||
		strings.Contains(combined, "quran") {
		selectedIDs["arabic-waqf-craftsperson"] = true
	}

	// Conversion / Pricing / Sales / Checkout
	if strings.Contains(combined, "pricing") || strings.Contains(combined, "checkout") ||
		strings.Contains(combined, "billing") || strings.Contains(combined, "subscribe") ||
		strings.Contains(combined, "pay") || strings.Contains(combined, "cart") ||
		strings.Contains(combined, "upgrade") {
		selectedIDs["conversion-copywriter"] = true
		selectedIDs["enterprise-sales-investor"] = true
		selectedIDs["negative-churn-risk-customer"] = true
		selectedIDs["growth-product-strategist"] = true
	}

	// Legal / Compliance / Privacy / Healthcare
	if strings.Contains(combined, "terms") || strings.Contains(combined, "privacy") ||
		strings.Contains(combined, "gdpr") || strings.Contains(combined, "compliance") ||
		strings.Contains(combined, "policy") || strings.Contains(combined, "legal") {
		selectedIDs["legal-compliance-counsel"] = true
	}
	if strings.Contains(combined, "hipaa") || strings.Contains(combined, "medical") ||
		strings.Contains(combined, "health") || strings.Contains(combined, "patient") {
		selectedIDs["hipaa-healthcare-compliance"] = true
	}

	// Web3 & Crypto
	if strings.Contains(combined, "crypto") || strings.Contains(combined, "web3") ||
		strings.Contains(combined, "wallet") || strings.Contains(combined, "solidity") {
		selectedIDs["web3-contract-auditor"] = true
	}

	// SEO & Discoverability
	if strings.Contains(combined, "seo") || strings.Contains(combined, "serp") ||
		strings.Contains(combined, "schema") || strings.Contains(combined, "sitemap") {
		selectedIDs["seo-discoverability-auditor"] = true
	}

	// i18n & Localization
	if strings.Contains(combined, "i18n") || strings.Contains(combined, "localization") ||
		strings.Contains(combined, "multilingual") {
		selectedIDs["i18n-localization-architect"] = true
	}

	// AI Agent Readiness, Protocols & Synthetic Discovery
	if strings.Contains(combined, "agent") || strings.Contains(combined, "mcp") ||
		strings.Contains(combined, "llms.txt") || strings.Contains(combined, "markdown negotiation") ||
		strings.Contains(combined, "x402") || strings.Contains(combined, "mpp") ||
		strings.Contains(combined, "webmcp") || strings.Contains(combined, "a2a") ||
		strings.Contains(combined, "isitagentready") || strings.Contains(combined, "ai-ready") ||
		strings.Contains(combined, "bot auth") || strings.Contains(combined, "content signals") {
		selectedIDs["agent-readiness-architect"] = true
		selectedIDs["content-negotiation-token-economist"] = true
		selectedIDs["machine-identity-auth-counsel"] = true
		selectedIDs["agentic-commerce-auditor"] = true
		selectedIDs["seo-discoverability-auditor"] = true
	}

	// Complex Dashboard / Power Tools / Keyboard Heavy
	if strings.Contains(combined, "dashboard") || strings.Contains(combined, "console") ||
		strings.Contains(combined, "admin") || strings.Contains(combined, "editor") ||
		strings.Contains(combined, "app") {
		selectedIDs["keyboard-power-maximalist"] = true
		selectedIDs["performance-sentinel"] = true
	}

	// Auth / Onboarding / Public Portal
	if strings.Contains(combined, "login") || strings.Contains(combined, "auth") ||
		strings.Contains(combined, "signup") || strings.Contains(combined, "register") ||
		strings.Contains(combined, "welcome") || strings.Contains(combined, "onboard") {
		selectedIDs["first-time-user-advocate"] = true
		selectedIDs["security-compliance-sentry"] = true
	}

	// Default fallback if fewer than 4 selected: add first-time user and copywriter
	if len(selectedIDs) < 4 {
		selectedIDs["first-time-user-advocate"] = true
		selectedIDs["conversion-copywriter"] = true
	}

	res := make([]persona.Persona, 0, len(selectedIDs))
	for id := range selectedIDs {
		if p, ok := idMap[id]; ok {
			res = append(res, p)
		}
	}
	return res
}

// PersonaProposal represents a dynamically discovered persona gap.
type PersonaProposal struct {
	ID               string   `json:"id"`
	Name             string   `json:"name"`
	Domain           string   `json:"domain"`
	Tags             []string `json:"tags"`
	SystemPrompt     string   `json:"system_prompt"`
	SkillsInvoked    []string `json:"skills_invoked"`
	EvaluationRubric string   `json:"evaluation_rubric"`
	Reason           string   `json:"reason"`
}

// ToPersona converts the proposal into a persistent Persona struct.
func (p PersonaProposal) ToPersona() persona.Persona {
	return persona.Persona{
		ID:               p.ID,
		Name:             p.Name,
		Domain:           p.Domain,
		Tags:             p.Tags,
		SystemPrompt:     p.SystemPrompt,
		SkillsInvoked:    p.SkillsInvoked,
		EvaluationRubric: p.EvaluationRubric,
		NegativeLessons:  []string{},
		UsageCount:       0,
		ApprovalRate:     "100.0%",
	}
}

// DetectGaps evaluates context against the existing roster to suggest missing personas.
func DetectGaps(existing []persona.Persona, contextText string) []PersonaProposal {
	existingIDs := make(map[string]bool, len(existing))
	for _, p := range existing {
		existingIDs[p.ID] = true
	}

	lower := strings.ToLower(contextText)
	var proposals []PersonaProposal

	// 1. Mobile native / touch gesture gap
	if (strings.Contains(lower, "mobile app") || strings.Contains(lower, "ios") ||
		strings.Contains(lower, "android") || strings.Contains(lower, "touch gesture") ||
		strings.Contains(lower, "flutter") || strings.Contains(lower, "react native")) &&
		!existingIDs["mobile-native-ergonomist"] {
		proposals = append(proposals, PersonaProposal{
			ID:     "mobile-native-ergonomist",
			Name:   "Mobile Native & Touch Ergonomist",
			Domain: "mobile",
			Tags:   []string{"ios", "android", "mobile", "gestures", "responsive"},
			SystemPrompt: "You are a Lead Mobile Native UX Engineer specializing in iOS and Android touch ergonomics, " +
				"haptic feedback, thumb-zone reachability, swipe gestures, and responsive mobile viewport performance.",
			SkillsInvoked: []string{"browser-testing-with-devtools"},
			EvaluationRubric: "- [ ] Audit thumb-zone reachability on mobile viewport\n" +
				"- [ ] Verify swipe gesture responsiveness and swipe-to-dismiss behavior\n" +
				"- [ ] Check notch, dynamic island, and bottom safe-area insets\n" +
				"- [ ] Ensure touch target sizing meets 48x48dp mobile standard",
			Reason: fmt.Sprintf("Context mentions mobile/native touch surfaces not fully covered by web UI personas"),
		})
	}

	// 2. Healthcare / HIPAA / Patient privacy gap
	if (strings.Contains(lower, "hipaa") || strings.Contains(lower, "patient") ||
		strings.Contains(lower, "ehr") || strings.Contains(lower, "medical") ||
		strings.Contains(lower, "health record")) &&
		!existingIDs["hipaa-healthcare-compliance"] {
		proposals = append(proposals, PersonaProposal{
			ID:     "hipaa-healthcare-compliance",
			Name:   "HIPAA & Clinical Data Privacy Specialist",
			Domain: "healthcare",
			Tags:   []string{"hipaa", "phi", "privacy", "clinical", "security"},
			SystemPrompt: "You are a Senior Healthcare Compliance and Clinical Data Privacy Auditor. " +
				"You inspect interfaces for Protected Health Information (PHI) exposure, auto-logout on idle, " +
				"patient consent captures, and audit trails required under HIPAA Security & Privacy Rules.",
			SkillsInvoked: []string{"security-and-hardening"},
			EvaluationRubric: "- [ ] Ensure zero PHI leakage in URL parameters or browser tab titles\n" +
				"- [ ] Verify idle timeout screen lock triggers after inactivity\n" +
				"- [ ] Inspect patient consent dialogues for clear withdrawal options\n" +
				"- [ ] Check encryption in transit indicators and SSL/TLS pinning\n" +
				"- [ ] Verify HIPAA Business Associate Agreement (BAA) terms under 45 CFR § 164.504(e)\n" +
				"- [ ] Confirm statutory breach notification and PHI return/destruction clauses on termination",
			Reason: fmt.Sprintf("Context indicates healthcare or medical data handling requiring strict HIPAA safeguards"),
		})
	}

	// 3. Web3 / Crypto / Smart contract gap
	if (strings.Contains(lower, "web3") || strings.Contains(lower, "crypto") ||
		strings.Contains(lower, "wallet") || strings.Contains(lower, "solidity") ||
		strings.Contains(lower, "token") || strings.Contains(lower, "blockchain")) &&
		!existingIDs["web3-contract-auditor"] {
		proposals = append(proposals, PersonaProposal{
			ID:     "web3-contract-auditor",
			Name:   "Web3 Smart Contract & Wallet Ergonomist",
			Domain: "web3",
			Tags:   []string{"web3", "wallet", "blockchain", "crypto", "defi"},
			SystemPrompt: "You are a Web3 Ergonomics & Wallet Security Auditor. " +
				"You examine transaction simulation clarity, gas fee previews, network switching prompts, " +
				"slippage tolerances, and phishing safeguards during wallet interaction.",
			SkillsInvoked: []string{"security-and-hardening"},
			EvaluationRubric: "- [ ] Check clear human-readable transaction preview before wallet sign\n" +
				"- [ ] Verify unambiguous gas fee and maximum slippage display\n" +
				"- [ ] Inspect error recovery when user rejects a wallet signature\n" +
				"- [ ] Confirm network switch requests clearly indicate chain ID and RPC target",
			Reason: fmt.Sprintf("Context involves crypto/Web3 transactions with irreversible financial impact"),
		})
	}

	// 4. SEO & Organic Discoverability gap
	if (strings.Contains(lower, "seo") || strings.Contains(lower, "schema.org") ||
		strings.Contains(lower, "sitemap") || strings.Contains(lower, "search engine") ||
		strings.Contains(lower, "open graph") || strings.Contains(lower, "serp")) &&
		!existingIDs["seo-discoverability-auditor"] {
		proposals = append(proposals, PersonaProposal{
			ID:     "seo-discoverability-auditor",
			Name:   "Search Engine Discoverability & Meta Architect",
			Domain: "marketing",
			Tags:   []string{"seo", "serp", "opengraph", "metadata", "schema"},
			SystemPrompt: "You are a Senior Technical SEO and Organic Discoverability Architect. " +
				"You audit Open Graph tags, canonical URLs, JSON-LD structured data, robots directives, " +
				"and server-side rendering crawlability for search indexers.",
			SkillsInvoked: []string{"read_url_content"},
			EvaluationRubric: "- [ ] Inspect title tag length (<60 chars) and unique meta description\n" +
				"- [ ] Verify Open Graph image (1200x630) and twitter:card meta tags\n" +
				"- [ ] Check JSON-LD structured data schema syntax validity\n" +
				"- [ ] Ensure canonical link tags avoid duplicate content penalties",
			Reason: fmt.Sprintf("Context targets search engine visibility and structured schema crawlability"),
		})
	}

	// 5. Global i18n & Bidirectional RTL gap
	if (strings.Contains(lower, "i18n") || strings.Contains(lower, "localization") ||
		strings.Contains(lower, "multilingual") || strings.Contains(lower, "rtl layout") ||
		strings.Contains(lower, "translation")) &&
		!existingIDs["i18n-localization-architect"] {
		proposals = append(proposals, PersonaProposal{
			ID:     "i18n-localization-architect",
			Name:   "Global i18n & Bidirectional RTL Architect",
			Domain: "localization",
			Tags:   []string{"i18n", "l10n", "rtl", "bidi", "multilingual"},
			SystemPrompt: "You are an Internationalization (i18n) and Bidirectional (RTL/LTR) Layout Architect. " +
				"You inspect text expansion tolerance, logical CSS properties (margin-inline-start), " +
				"date/number locale formatting, and cultural icon appropriateness.",
			SkillsInvoked: []string{"waqftech-writing"},
			EvaluationRubric: "- [ ] Verify UI does not truncate under 30% German/Spanish text expansion\n" +
				"- [ ] Check CSS uses logical properties (start/end) instead of hardcoded left/right\n" +
				"- [ ] Verify currencies and calendar dates adapt to viewer locale\n" +
				"- [ ] Ensure chevron and directional arrows flip appropriately in RTL mode",
			Reason: fmt.Sprintf("Context involves multilingual audience and bidirectional layout needs"),
		})
	}

	// 6. AI Agent Readiness & Synthetic Interoperability gap
	if (strings.Contains(lower, "agent-ready") || strings.Contains(lower, "mcp server") ||
		strings.Contains(lower, "a2a") || strings.Contains(lower, "webmcp") ||
		strings.Contains(lower, "ai-catalog") || strings.Contains(lower, "ard")) &&
		!existingIDs["agent-readiness-architect"] {
		proposals = append(proposals, PersonaProposal{
			ID:            "agent-readiness-architect",
			Name:          "Principal AI Agent Readiness & Synthetic Interoperability Architect",
			Domain:        "protocol",
			Tags:          []string{"agent-ready", "mcp", "a2a", "agent-skills", "rfc9728", "webmcp", "ard"},
			SystemPrompt:  "You are a Principal AI Agent Readiness Architect. You evaluate websites, applications, and APIs for synthetic interoperability and autonomous agent readiness. You inspect MCP Server Cards (SEP-1649), A2A Agent Cards, Agent Skills Discovery (RFC v0.2.0), ARD capability manifests, WebMCP in-browser tool declarations (navigator.modelContext.provideContext), RFC 9727 API Catalogs, RFC 9728 OAuth Protected Resource Metadata, and DNS-AID discovery records.",
			SkillsInvoked: []string{"api-and-interface-design", "read_url_content"},
			EvaluationRubric: "- [ ] Check for MCP Server Card at /.well-known/mcp/server-card.json (SEP-1649)\n" +
				"- [ ] Inspect A2A Agent Card at /.well-known/agent-card.json with supported interfaces\n" +
				"- [ ] Verify Agent Skills discovery index at /.well-known/agent-skills/index.json (RFC v0.2.0)\n" +
				"- [ ] Check ARD manifest at /.well-known/ai-catalog.json with URN identifiers\n" +
				"- [ ] Verify WebMCP browser tool exposure (navigator.modelContext.provideContext)\n" +
				"- [ ] Inspect RFC 9727 API catalog at /.well-known/api-catalog with OpenAPI links\n" +
				"- [ ] Verify RFC 9728 OAuth Protected Resource Metadata at /.well-known/oauth-protected-resource",
			Reason: fmt.Sprintf("Context targets autonomous AI agent protocol discovery and synthetic interoperability"),
		})
	}

	// 7. Agentic Commerce & Machine Payments gap
	if (strings.Contains(lower, "x402") || strings.Contains(lower, "mpp") ||
		strings.Contains(lower, "agent payment") || strings.Contains(lower, "machine payment") ||
		strings.Contains(lower, "agentic commerce") || strings.Contains(lower, "ucp") ||
		strings.Contains(lower, "acp")) &&
		!existingIDs["agentic-commerce-auditor"] {
		proposals = append(proposals, PersonaProposal{
			ID:            "agentic-commerce-auditor",
			Name:          "Agentic Commerce & Machine Payment Protocols (MPP/x402/ACP) Specialist",
			Domain:        "commerce",
			Tags:          []string{"commerce", "x402", "mpp", "acp", "ucp", "micropayments", "agent-checkout"},
			SystemPrompt:  "You are an Agentic Commerce & Machine Payments Specialist. You audit autonomous financial interaction surfaces, HTTP 402 payment requirements, machine checkout protocols, and automated budget safeguards. You evaluate Coinbase x402 protocol integration (@x402/hono, @x402/express), Machine Payment Protocol (mpp.dev) with x-payment-info OpenAPI extensions, Universal Commerce Protocol (UCP), Agentic Commerce Protocol (ACP at /.well-known/acp.json), and programmatic transaction limits.",
			SkillsInvoked: []string{"security-and-hardening", "api-and-interface-design"},
			EvaluationRubric: "- [ ] Check HTTP 402 Payment Required response headers and facilitator contracts (x402)\n" +
				"- [ ] Inspect OpenAPI operations for x-payment-info extensions declaring intent, method, and currency (MPP)\n" +
				"- [ ] Verify ACP discovery document at /.well-known/acp.json declaring protocol version and transports\n" +
				"- [ ] Verify UCP endpoint at /.well-known/ucp with reachability of schema URLs\n" +
				"- [ ] Ensure machine payment routes enforce max transaction caps and replay protection",
			Reason: fmt.Sprintf("Context involves autonomous agent payments and machine commerce protocols"),
		})
	}

	// 8. Agent Content Accessibility & Token Economics gap
	if (strings.Contains(lower, "markdown negotiation") || strings.Contains(lower, "llms.txt") ||
		strings.Contains(lower, "content signals") || strings.Contains(lower, "token economics") ||
		strings.Contains(lower, "crawler policy")) &&
		!existingIDs["content-negotiation-token-economist"] {
		proposals = append(proposals, PersonaProposal{
			ID:            "content-negotiation-token-economist",
			Name:          "Agent Content Accessibility & Token Economics Engineer",
			Domain:        "content",
			Tags:          []string{"markdown-negotiation", "llms-txt", "token-economics", "content-signals", "crawler-policy"},
			SystemPrompt:  "You are an Agent Content Accessibility and Token Economics Engineer. You evaluate how efficiently LLM agents and web crawlers can consume your site's information. You inspect HTTP Accept: text/markdown content negotiation, x-markdown-tokens response headers, llms.txt and llms-full.txt curation per llmstxt.org, Content-Signal directives in robots.txt (ai-train, search, ai-input), and the elimination of DOM bloat, hydration payloads, and script noise in agent-facing responses.",
			SkillsInvoked: []string{"read_url_content", "code-simplification"},
			EvaluationRubric: "- [ ] Verify Accept: text/markdown returns Content-Type: text/markdown with clean Markdown\n" +
				"- [ ] Check for x-markdown-tokens header in Markdown responses to signal prompt budget size\n" +
				"- [ ] Verify /llms.txt and /llms-full.txt exist and provide concise, structured site summaries\n" +
				"- [ ] Inspect robots.txt for Content-Signal directives (e.g. Content-Signal: search=yes, ai-train=no)\n" +
				"- [ ] Audit AI crawler user-agent rules (GPTBot, Claude-Web, OAI-SearchBot, Google-Extended)\n" +
				"- [ ] Ensure markdown responses strip navbars, footers, cookie banners, and hydration state",
			Reason: fmt.Sprintf("Context indicates need for token-efficient content negotiation and agent crawler governance"),
		})
	}

	// 9. Autonomous Agent Authentication & Machine Identity gap
	if (strings.Contains(lower, "auth.md") || strings.Contains(lower, "web bot auth") ||
		strings.Contains(lower, "id-jag") || strings.Contains(lower, "machine identity") ||
		strings.Contains(lower, "agent auth")) &&
		!existingIDs["machine-identity-auth-counsel"] {
		proposals = append(proposals, PersonaProposal{
			ID:            "machine-identity-auth-counsel",
			Name:          "Autonomous Agent Authentication & Machine Identity Specialist",
			Domain:        "auth",
			Tags:          []string{"auth-md", "oauth2", "web-bot-auth", "id-jag", "rfc8414", "rfc9728", "machine-identity"},
			SystemPrompt:  "You are an Autonomous Agent Authentication and Machine Identity Specialist. You evaluate how AI agents securely identify, authenticate, and register themselves with APIs and protected resources. You audit Auth.md (/auth.md) agent registration manifests, Web Bot Auth HTTP Message Signatures (/.well-known/http-message-signatures-directory), RFC 8414 OAuth Authorization Server metadata with agent_auth blocks, RFC 9728 Protected Resource Metadata, and identity assertion standards like ID-JAG (urn:ietf:params:oauth:token-type:id-jag).",
			SkillsInvoked: []string{"security-and-hardening", "credentials"},
			EvaluationRubric: "- [ ] Verify /auth.md exists with clear H1 header and programmatic agent onboarding instructions\n" +
				"- [ ] Check /.well-known/oauth-authorization-server contains agent_auth block with register_uri\n" +
				"- [ ] Inspect Web Bot Auth directory at /.well-known/http-message-signatures-directory for signed requests\n" +
				"- [ ] Verify RFC 9728 Protected Resource Metadata matches authorization server issuer and scopes\n" +
				"- [ ] Check support for ID-JAG token assertions (identity_types_supported: [\"identity_assertion\"])\n" +
				"- [ ] Ensure revocation endpoints and credential rotation mechanisms are documented for machines",
			Reason: fmt.Sprintf("Context requires machine-to-machine authentication, programmatic agent onboarding, and Web Bot Auth"),
		})
	}

	return proposals
}
