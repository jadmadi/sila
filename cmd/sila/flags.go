package main

import (
	"flag"

	"github.com/jadmadi/sila/internal/store"
)

// writeVisibility carries the --visibility scope into memory write commands
// after the shared flag parser has consumed it.
var writeVisibility string

type rootFlags struct {
	project                *string
	projectAlias           *string
	globalFlag             *bool
	jsonOut                *bool
	jsonEnvelopeFlag       *bool
	toonOut                *bool
	quietFlag              *bool
	porcelainFlag          *bool
	syncFlag               *bool
	relativePathsFlag      *bool
	advisoryFlag           *bool
	humanOut               *bool
	activityFlag           *bool
	verbose                *bool
	verboseAlias           *bool
	noColor                *bool
	toolFlag               *string
	toolAlias              *string
	role                   *string
	refsExistOnly          *bool
	factsOnly              *bool
	staleOnly              *bool
	llmProvider            *string
	retryFailed            *bool
	top                    *int
	includeAmbiguousFlag   *bool
	codeOnlyFlag           *bool
	maxHopsFlag            *int
	maxDepthFlag           *int
	scan                   *bool
	since                  *string
	minSessions            *int
	tagFlag                *string
	tag                    *string
	writeFlag              *bool
	unreleasedFlag         *bool
	agent                  *string
	categoryFlag           *string
	idFlag                 *string
	branch                 *string
	openItems              *string
	openFeedback           *bool
	closedFeedback         *bool
	decisions              *string
	files                  *string
	visibilityFlag         *string
	title                  *string
	decidersFlag           *string
	decisionFlag           *string
	lang                   *string
	repo                   *string
	repoCleanFlag          *bool
	ext                    *string
	eco                    *string
	crawl                  *bool
	depth                  *int
	maxPages               *int
	limitFlag              *int
	autoIngest             *bool
	autoSync               *bool
	watchFlag              *bool
	intervalFlag           *string
	compactFlag            *bool
	lineFlag               *bool
	plainFlag              *bool
	asciiFlag              *bool
	changedOnlyFlag        *bool
	adoptionFlag           *bool
	pendingFlag            *bool
	claimedFlag            *bool
	claimsFlag             *bool
	olderThanFlag          *string
	convertTasksFlag       *bool
	fromFlag               *string
	againFlag              *bool
	forceSimilarFlag       *bool
	includeInstructions    *bool
	maxFilesFlag           *int
	strictFlag             *bool
	requireTestsFlag       *bool
	widthFlag              *int
	strictMemoryFlag       *bool
	noMemoryFlag           *bool
	memoTextFlag           *string
	memoClaimFlag          *string
	stopPolicyFlag         *string
	stopEnforceFlag        *bool
	yesFlag                *bool
	includeTranscriptsFlag *bool
	issueFlag              *bool
	shareFlag              *bool
	tokenFlag              *string
	remoteURLFlag          *string
	readyFlag              *bool
	blockedFlag            *bool
	allGoalsFlag           *bool
	allCleanFlag           *bool
	allChangelogFlag       *bool
	allAgentsFlag          *bool
	timelineFlag           *bool
	implementedFlag        *bool
	independentFlag        *bool
	dependentFlag          *bool
	descFlag               *string
	dependsOnFlag          *string
	addDepFlag             *string
	removeDepFlag          *string
	seqFlag                *int
	touchFlag              *string
	shapeFlag              *string
	noteFlag               *string
	goalFlag               *string
	goalsFlag              *string
	goalsAuditFlag         *bool
	sessionFlag            *string
	statusFlag             *string
	tierFlag               *string
	allowFlag              *string
	denyFlag               *string
	durationFlag           *string
	invariantsFlag         *string
	tasksFlag              *string
	workPackageFlag        *string
	specReviewFlag         *string
	goalReviewFlag         *string
	recallTaskFlag         *string
	recallPathsFlag        *string
	recallSymbolsFlag      *string
	recallHeadFlag         *string
	recallContextFile      *string
	selectionFlag          *string
	autoRunFlag            *bool
	taskFlag               *string
	causeFlag              *string
	fixFlag                *string
	unslopFixFlag          *bool
	unslopDiffFlag         *bool
	approachFlag           *string
	tagsFlag               *string
	domainFlag             *string
	symptomFlag            *string
	ruleFlag               *string
	stackFlag              *string
	principlesFlag         *bool
	stateBlockFlag         *bool
	saveFlag               *bool
	reasonFlag             *string
	claimImmediateFlag     *bool
	stageFlag              *bool
	commitFlag             *string
	noBuildFlag            *bool
	checkTasksFlag         *bool
	noSyncFlag             *bool
	manifestFlag           *string
	statsFlag              *bool
	byFlag                 *string
	rejectFlag             *bool
	evidenceFlag           *string
	fullFlag               *bool
	forceFlag              *bool
	ifStaleFlag            *bool
	checkOnlyFlag          *bool
	sourceFlag             *bool
	repairFlag             *bool
	skillsFlag             *bool
	mcpFlag                *bool
	skillsListFlag         *string
	excludeFlag            *string
	covenantFlag           *bool
	edgeFlag               *bool
	debtFlag               *bool
	memoryFlag             *bool
	scarTissueFlag         *bool
	docCoverageFlag        *bool
	reportCardFlag         *bool
	coverageFlag           *bool
	profileFlag            *string
	mcpSchemaFlag          *bool
	mcpTraceFlag           *bool
	preCommitFlag          *bool
	skipArchFlag           *bool
	budgetFlag             *string
	maxTokensFlag          *string
	pressureFlag           *string
	includeMarkdownFlag    *bool
	markdownFlag           *reportMarkdown
	cachePinnedFlag        *bool
	primeAllGoalsFlag      *bool
	noMapFlag              *bool
	mapCharsFlag           *int
	eventFlag              *string
	hookNoAutoHandoff      *bool
	payloadFlag            *string
	drainFlag              *bool
	cwdFlag                *string
	hookFlag               *bool
	remoteFlag             *bool
	forwardFlag            *string
	noHooksFlag            *bool
	noMCPFlag              *bool
	shimFlag               *bool
	runFlag                *bool
	dryRunFlag             *bool
	cloudSyncFlag          *bool
	toolsFlag              *string
	replaceFlag            *bool
	policyPackFlag         *string
	omniFlag               *bool
	aclScopedFlag          *bool
	semanticFlag           *bool
	windowFlag             *int
	occurrenceFlag         *int
	twoPassFlag            *bool
	noTwoPassFlag          *bool
	target                 *string
	reviewFlag             *bool
	scopeFlag              *string
	portFlag               *int
	hostFlag               *string
	exposeFlag             *bool
	noOpenFlag             *bool
	demoFlag               *bool
	webFlag                *bool
	keepFlag               *bool
	stepFlag               *string
	outFile                *string
	memoryFileFlag         *string
	dbPath                 *string
	revealFlag             *bool
	formatFlag             *string
	heavyCleanFlag         *bool
	harnessFlag            *string
	ablationFlag           *bool
	evalDirFlag            *string
	fromLessonFlag         *string
	fromFeedbackFlag       *string
	allLessonsFlag         *bool
	caseFlag               *string
	bareFlag               *bool
	reportFlag             *bool
	allowDirtyFlag         *bool
	offlineFlag            *bool
	promptOnlyFlag         *bool
	registerGoalsFlag      *bool
	squadFlag              *string
	personasFlag           *string
	pagesFlag              *string
	appFlag                *string
	recommendFlag          *string
	presetFlag             *string
	registerFlag           *bool
	contextFlag            *string
	groupFlag              *string
	interactiveFlag        *bool
	upstreamsFlag          *bool
	noUpstreamsFlag        *bool
	symbolFlag             *string
	kindFlag               *string
	exportFlag             *bool
	importFlag             *bool
	archiveAuthorityFlag   *string
	duplicateOfFlag        *string
	hardDeleteFlag         *bool
	revealSensitiveFlag    *bool
	includeArchivedFlag    *bool
	verifyFlag             *bool
	// Crew & lane flags
	briefFlag          *string
	surfaceFlag        *string
	noCCLIFlag         *bool
	ccliStrictExitFlag *bool
	noInputFlag        *bool
	confirmFlag        *string
	asOfFlag           *string
	postCommitFlag     *bool
	trustTargetFlag    *string
	showConfigFlag     *bool
}

func newRootFlags(fs *flag.FlagSet, sub string) *rootFlags {
	rf := &rootFlags{}
	rf.project = fs.String("project", "", "restrict to working dirs matching this project name/id")
	rf.projectAlias = fs.String("p", "", "shorthand for --project")
	rf.globalFlag = fs.Bool("global", false, "ignore the current directory and use every project")
	fs.BoolVar(rf.globalFlag, "g", false, "shorthand for --global")
	rf.jsonOut = fs.Bool("json", false, "emit machine-readable JSON on stdout")
	rf.jsonEnvelopeFlag = fs.Bool("json-envelope", false, "emit the versioned sila/json/v1 envelope on stdout (implies --json)")
	rf.toonOut = fs.Bool("toon", false, "emit compact Token-Oriented Object Notation (TOON) on stdout")
	rf.quietFlag = fs.Bool("quiet", false, "suppress human hints and success decoration; errors and exit codes are preserved")
	rf.porcelainFlag = fs.Bool("porcelain", false, "emit stable line-oriented output (implies --quiet; conflicts with --json, --json-envelope, and --toon)")
	rf.syncFlag = fs.Bool("sync", false, "allow machine-readable reads to reconcile goals/README.md and clear closed claims (goals --json --sync)")
	rf.relativePathsFlag = fs.Bool("relative-paths", false, "emit repository-relative paths in JSON where a command supports it")
	rf.advisoryFlag = fs.Bool("advisory", false, "report check failures without a non-zero exit (onboard check)")
	rf.humanOut = fs.Bool("human", false, "render the human executive briefing instead of the agent standup (digest)")
	rf.activityFlag = fs.Bool("activity", false, "show recent session activity (requests & replies) across tools for the project (digest)")
	rf.verbose = fs.Bool("verbose", false, "show full source paths/sizes, extra detail")
	rf.verboseAlias = fs.Bool("v", false, "shorthand for --verbose")
	rf.noColor = fs.Bool("no-color", false, "disable colored output")
	rf.toolFlag = fs.String("tool", "", "filter claims/messages by contributing tool (dream query, search) or select one tool for mcp --print-schema")
	rf.toolAlias = fs.String("t", "", "shorthand for --tool")
	rf.role = fs.String("role", "", "filter messages by role: user, assistant (search)")
	rf.refsExistOnly = fs.Bool("refs-exist", false, "only show claims whose referenced files exist on disk (dream query)")
	fs.BoolVar(rf.refsExistOnly, "verified-only", false, "deprecated alias for --refs-exist")
	rf.factsOnly = fs.Bool("facts-only", false, "only show promoted, LLM-distilled verified facts (dream query)")
	rf.staleOnly = fs.Bool("stale", false, "only show stale / unverified claims (dream query) or release stale claims (goals release --stale)")
	fs.BoolVar(rf.staleOnly, "unverified", false, "alias for --stale")
	rf.llmProvider = fs.String("llm", "", "distill pending claims with an LLM (e.g. --llm=stdio)")
	rf.retryFailed = fs.Bool("retry-failed", false, "retry failed LLM distillation attempts")
	rf.top = fs.Int("top", 0, "limit query/search results (0 = all)")
	rf.includeAmbiguousFlag = fs.Bool("include-ambiguous", false, "allow AMBIGUOUS edges in graph path queries")
	rf.codeOnlyFlag = fs.Bool("code-only", false, "restrict graph path, affected, and build to code nodes")
	rf.maxHopsFlag = fs.Int("max-hops", 0, "maximum hops for graph path search")
	rf.maxDepthFlag = fs.Int("max-depth", 0, "maximum depth for graph query and blast radius")
	rf.ifStaleFlag = fs.Bool("if-stale", false, "skip the graph build when the graph is already current")
	rf.scan = fs.Bool("scan", false, "status: live-scan tool sources for new messages (slower; ~10s)")
	rf.since = fs.String("since", "", "only messages at or after RFC3339 or a duration (e.g. 24h)")
	rf.minSessions = fs.Int("min-sessions", 0, "minimum project sessions to include in benchmark (sila benchmark)")
	rf.tagFlag = fs.String("tag-range", "", "git tag range for changelog (e.g. v0.9.0..HEAD) (sila changelog)")
	fs.StringVar(rf.tagFlag, "range", "", "alias for --tag-range (sila changelog)")
	if sub == "changelog" {
		rf.tag = rf.tagFlag
		fs.StringVar(rf.tagFlag, "tag", "", "alias for --tag-range (sila changelog)")
	} else {
		rf.tag = fs.String("tag", "", "tag filter for troubleshooting search (doc so)")
	}
	rf.writeFlag = fs.Bool("write", false, "write changes to CHANGELOG.md (sila changelog)")
	fs.BoolVar(rf.writeFlag, "w", false, "alias for --write (sila changelog)")
	rf.unreleasedFlag = fs.Bool("unreleased", false, "scan unreleased commits for changelog (sila changelog)")
	fs.BoolVar(rf.unreleasedFlag, "u", false, "alias for --unreleased (sila changelog)")
	fs.BoolVar(rf.scan, "s", false, "alias for --scan (status, changelog)")
	rf.agent = fs.String("agent", "", "author or target agent (e.g. devin, claude-code)")
	rf.categoryFlag = fs.String("type", "idea", "feedback category: idea, issue, tip, challenge, praise (feedback)")
	fs.StringVar(rf.categoryFlag, "category", "idea", "alias for --type (feedback)")
	rf.idFlag = fs.String("id", "", "explicit ticket id (e.g. dialectic-subshell-paths) (feedback)")
	rf.branch = fs.String("branch", "", "active git branch name (handoff)")
	if sub == "feedback" {
		rf.openFeedback = fs.Bool("open", false, "only list open feedback (feedback)")
	} else {
		rf.openItems = fs.String("open", "", "comma-separated list of open tasks/todos (handoff)")
	}
	rf.closedFeedback = fs.Bool("closed", false, "only list closed feedback (feedback)")
	rf.decisions = fs.String("decisions", "", "comma-separated list of key decisions (handoff)")
	rf.files = fs.String("files", "", "comma-separated list of referenced repo files (handoff, memo, adr new, graph slice)")
	rf.visibilityFlag = fs.String("visibility", "", "memory visibility scope: project (default) or global (memo, lesson, case)")
	rf.title = fs.String("title", "", "document title (doc)")
	rf.decidersFlag = fs.String("deciders", "", "decision participants / agent name (adr new, adr from-memo)")
	rf.decisionFlag = fs.String("decision", "", "addressed decision identifier (e.g. D1) (memo, decisions)")
	rf.lang = fs.String("lang", "", "programming language filter (doc code-search)")
	if sub == "clean" {
		rf.repoCleanFlag = fs.Bool("repo", false, "scope cleanup strictly to the active repository (clean)")
		fs.BoolVar(rf.repoCleanFlag, "r", false, "shorthand for --repo (clean)")
	} else {
		rf.repo = fs.String("repo", "", "repository filter (owner/repo) (doc code-search)")
	}
	rf.ext = fs.String("ext", "", "file extension filter (doc code-search)")
	rf.eco = fs.String("eco", "auto", "package registry ecosystem: auto, go, rust, npm, python (doc pkg)")
	rf.crawl = fs.Bool("crawl", false, "recursively crawl and ingest documentation site (doc)")
	rf.depth = fs.Int("depth", 2, "max crawl depth (doc)")
	rf.maxPages = fs.Int("max-pages", 30, "max pages to crawl (doc)")
	rf.limitFlag = fs.Int("limit", 50, "maximum items to pull or process (cloud pull)")
	rf.autoIngest = fs.Bool("auto-ingest", false, "automatically save search results (doc web-search, code-search, pkg, so)")
	rf.autoSync = fs.Bool("auto-sync", false, "trigger sync on session end (hook session-end)")
	rf.watchFlag = fs.Bool("watch", false, "run continuous live synchronization loop (sync)")
	fs.BoolVar(rf.watchFlag, "live", false, "alias for --watch (sync)")
	rf.intervalFlag = fs.String("interval", "15s", "polling interval for live sync (e.g. 10s, 30s, 1m) (sync)")
	rf.compactFlag = fs.Bool("compact", false, "run compact 1-2 line status ticker instead of full-screen live cockpit (watch)")
	rf.lineFlag = fs.Bool("line", false, "print goal stats as a single sidebar-sized line (goals stats)")
	rf.plainFlag = fs.Bool("plain", false, "disable colors and ANSI styling (stats); ASCII tags for goals")
	rf.asciiFlag = fs.Bool("ascii", false, "replace emoji with stable ASCII tags and bars (goals)")
	rf.changedOnlyFlag = fs.Bool("changed-only", false, "write only changed targets; default behavior for agents sync")
	rf.adoptionFlag = fs.Bool("adoption", false, "show the 7-day tool-call adoption funnel and advisory verdict (doctor)")
	rf.pendingFlag = fs.Bool("pending", false, "only list pending goals ready for execution (goals)")
	rf.claimedFlag = fs.Bool("claimed", false, "only list claimed/in-progress goals (goals)")
	rf.claimsFlag = fs.Bool("claims", false, "prune claim records on implemented or superseded goals (goals prune --claims)")
	rf.olderThanFlag = fs.String("older-than", "72h", "minimum claim age for goals release --stale (e.g. 48h, 3d)")
	rf.convertTasksFlag = fs.Bool("convert-tasks", false, "convert a markdown task list into a backlog goal (onboard)")
	rf.fromFlag = fs.String("from", "", "source format for --convert-tasks (onboard) or claude|codex|grok|all (import)")
	rf.againFlag = fs.Bool("again", false, "resend changed files under a fresh import key (import)")
	rf.forceSimilarFlag = fs.Bool("force-similar", false, "write near-duplicate imported memories instead of skipping them (import)")
	rf.includeInstructions = fs.Bool("include-instructions", false, "recognized only to refuse instruction import with IMPORT_INSTRUCTIONS_REFUSED (import)")
	rf.maxFilesFlag = fs.Int("max-files", 0, "maximum files to scan in one import run, 1-200 (import)")
	rf.strictFlag = fs.Bool("strict", false, "exit non-zero on critical onboarding failures (onboard check); promote unavailable gate checks (gate)")
	rf.requireTestsFlag = fs.Bool("require-tests", false, "treat a skipped test run as a blocking gate failure (gate)")
	rf.widthFlag = fs.Int("width", 0, "override the rendered width in columns (salam, prime; default: COLUMNS or 100)")
	rf.strictMemoryFlag = fs.Bool("strict-memory", false, "promote legacy Memory Protocol warnings to errors (goals validate)")
	rf.noMemoryFlag = fs.Bool("no-memory", false, "waive required memory evidence; --reason is mandatory (goals certify)")
	rf.memoTextFlag = fs.String("text", "", "literal memo text; bypasses subcommand dispatch (memo)")
	rf.memoClaimFlag = fs.String("claim-text", "", "filter memos by exact claim text; useful for the historical 'list' trap (memo list)")
	rf.stopPolicyFlag = fs.String("policy", "", "stop-gate policy: off, warn, or enforce (hook stop/session-end)")
	rf.stopEnforceFlag = fs.Bool("enforce", false, "enforce the stop memory gate for this invocation (hook stop/session-end)")
	rf.includeTranscriptsFlag = fs.Bool("include-transcripts", false, "also exclude stored messages from search and recall (forget)")
	rf.yesFlag = fs.Bool("yes", false, "confirm action without interactive confirmation prompt (import, onboard --convert-tasks, feedback --issue/submit, skill apply, forget)")
	fs.BoolVar(rf.yesFlag, "y", false, "alias for --yes")
	rf.issueFlag = fs.Bool("issue", false, "submit feedback upstream to GitHub Issues (feedback)")
	fs.BoolVar(rf.issueFlag, "github", false, "alias for --issue (feedback)")
	fs.BoolVar(rf.issueFlag, "gh", false, "alias for --issue (feedback)")
	rf.shareFlag = fs.Bool("share", false, "share feedback anonymously with Sila maintainer via Cloudflare D1 (feedback)")
	fs.BoolVar(rf.shareFlag, "d1", false, "alias for --share (feedback)")
	rf.tokenFlag = fs.String("token", "", "authentication token for edge feedback drain (feedback drain)")
	if sub == "feedback" {
		rf.remoteURLFlag = fs.String("remote", "", "remote edge URL (feedback)")
	}
	rf.readyFlag = fs.Bool("ready", false, "only list unblocked goals ready to pick (goals)")
	rf.blockedFlag = fs.Bool("blocked", false, "only list goals blocked by prerequisites (goals)")
	rf.timelineFlag = fs.Bool("timeline", false, "display compact release matrix timeline (changelog)")
	fs.BoolVar(rf.timelineFlag, "oneline", false, "alias for --timeline (changelog)")
	if sub == "clean" {
		rf.allCleanFlag = fs.Bool("all", false, "clean both repository residue and global toolchain caches (clean)")
		fs.BoolVar(rf.allCleanFlag, "a", false, "shorthand for --all (clean)")
	} else if sub == "changelog" {
		rf.allChangelogFlag = fs.Bool("all", false, "show all releases in changelog history (changelog)")
		fs.BoolVar(rf.allChangelogFlag, "a", false, "shorthand for --all (changelog)")
	} else if sub == "agents" {
		rf.allAgentsFlag = fs.Bool("all", false, "deploy or audit rules across all registered workspace repositories (agents)")
		fs.BoolVar(rf.allAgentsFlag, "a", false, "shorthand for --all (agents)")
	} else {
		rf.allGoalsFlag = fs.Bool("all", false, "show full uncapped history of implemented goals (goals)")
	}
	rf.implementedFlag = fs.Bool("implemented", false, "only list implemented goals (goals)")
	fs.BoolVar(rf.implementedFlag, "done", false, "alias for --implemented (goals)")
	rf.independentFlag = fs.Bool("independent", false, "mark goal as independent/disjoint (goals create/update)")
	rf.dependentFlag = fs.Bool("dependent", false, "mark goal as dependent on prerequisites (goals update)")
	rf.descFlag = fs.String("desc", "", "goal description or summary (goals create/update)")
	fs.StringVar(rf.descFlag, "description", "", "alias for --desc (goals create/update)")
	rf.dependsOnFlag = fs.String("depends-on", "", "comma-separated goal slugs this goal depends on (goals create/update)")
	fs.StringVar(rf.dependsOnFlag, "deps", "", "alias for --depends-on (goals create/update)")
	fs.StringVar(rf.dependsOnFlag, "set", "", "alias for --depends-on (goals depend)")
	rf.addDepFlag = fs.String("add-dep", "", "comma-separated goal slugs to add as dependencies (goals update)")
	fs.StringVar(rf.addDepFlag, "add", "", "alias for --add-dep (goals update)")
	rf.removeDepFlag = fs.String("remove-dep", "", "comma-separated goal slugs to remove from dependencies (goals update)")
	fs.StringVar(rf.removeDepFlag, "remove", "", "alias for --remove-dep (goals update)")
	rf.seqFlag = fs.Int("seq", 0, "recommended sequence index for execution (goals create/update)")
	fs.IntVar(rf.seqFlag, "sequence", 0, "alias for --seq (goals create/update)")
	rf.touchFlag = fs.String("touch", "", "comma-separated target file or directory paths to touch (goals create/update)")
	fs.StringVar(rf.touchFlag, "files-to-touch", "", "alias for --touch (goals create/update)")
	rf.shapeFlag = fs.String("shape", "", "task shape: ship (code modification, default) or scout (non-mutating investigation) (goals create/update)")
	rf.noteFlag = fs.String("note", "", "progress report note or status update (goals claim/report, feedback resolve)")
	rf.goalFlag = fs.String("goal", "", "goal slug or ID (goals, feedback resolve, hire assess)")
	if sub == "crew" {
		rf.goalsFlag = fs.String("goals", "", "candidate goal slug or 'pending' (crew plan-lanes)")
	} else {
		rf.goalsAuditFlag = fs.Bool("goals", false, "audit squad coverage against active roadmap goals (hire assess)")
	}
	rf.sessionFlag = fs.String("session", "", "agent session id bound to a goal (goals focus/claim/active)")
	rf.statusFlag = fs.String("status", "", "structured status (goals report/status, feedback list)")
	rf.tierFlag = fs.String("tier", "", "goal tier filter or classification: immediate, roadmap, nice-to-have, wont-fix (goals, goals create/update)")
	rf.allowFlag = fs.String("allow", "", "comma-separated authorized mutation paths (goals claim)")
	rf.denyFlag = fs.String("deny", "", "comma-separated boundary paths to forbid (goals claim)")
	rf.durationFlag = fs.String("duration", "", "auto-release duration e.g. 2h (goals claim)")
	rf.invariantsFlag = fs.String("invariants", "", "semicolon-separated architectural invariants and constraints for facts.md (goals create)")
	fs.StringVar(rf.invariantsFlag, "invariant", "", "alias for --invariants (goals create)")
	rf.tasksFlag = fs.String("tasks", "", "semicolon-separated execution tasks or phases for plan.md (goals create)")
	rf.workPackageFlag = fs.String("work-package", "", "repo-relative or absolute path to work package JSON contract (goals create)")
	rf.specReviewFlag = fs.String("spec-review", "", "repo-relative or absolute path to spec review JSON contract (goals create/update)")
	if sub == "goals" {
		rf.goalReviewFlag = fs.String("review", "", "repo-relative or absolute path to spec review JSON contract to validate and import (goals readiness)")
	} else {
		emptyReview := ""
		rf.goalReviewFlag = &emptyReview
	}
	rf.selectionFlag = fs.String("selection", "", "receipt selection artifact path for task acceptance")
	rf.autoRunFlag = fs.Bool("auto-run", false, "automatically run task verification commands during goal certification")
	if sub == "crew" {
		rf.taskFlag = fs.String("task", "", "candidate goal slug or comma-separated goal slugs (crew plan-lanes)")
	} else if sub == "recall" {
		rf.recallTaskFlag = fs.String("task", "", "active task description for context-aware recall (recall)")
		rf.taskFlag = rf.recallTaskFlag
	} else {
		rf.taskFlag = rf.tasksFlag
		fs.StringVar(rf.tasksFlag, "task", "", "alias for --tasks (goals create)")
	}
	rf.causeFlag = fs.String("cause", "", "root cause of a solved problem (lesson)")
	if sub == "unslop" {
		rf.unslopFixFlag = fs.Bool("fix", false, "automatically fix conversational tells and format text (unslop)")
		rf.unslopDiffFlag = fs.Bool("diff", false, "display unified diff of proposed changes (unslop)")
	} else {
		rf.fixFlag = fs.String("fix", "", "what fixed the problem (lesson)")
	}
	rf.approachFlag = fs.String("approach", "", "structural or design pattern approach (case)")
	rf.tagsFlag = fs.String("tags", "", "comma-separated tags (lesson list filter, lesson record, case)")
	rf.domainFlag = fs.String("domain", "", "operational domain category (guide, learn)")
	rf.symptomFlag = fs.String("symptom", "", "symptom or failure mode (learn add)")
	rf.ruleFlag = fs.String("rule", "", "durable guardrail invariant rule (learn add)")
	rf.stackFlag = fs.String("stack", "", "technology stack (learn add, agents sync)")
	fs.StringVar(rf.stackFlag, "tech-stack", "", "alias for --stack (learn add, agents sync)")
	rf.principlesFlag = fs.Bool("principles", false, "include Universal Principles Pack (Layered Constitution & 9-verb memory loop) (agents sync, agents review)")
	rf.stateBlockFlag = fs.Bool("state-block", false, "write the continuity state block into AGENTS.md (agents sync)")
	rf.saveFlag = fs.Bool("save", false, "save distilled lessons into store (lesson distill)")
	rf.reasonFlag = fs.String("reason", "", "reopening reason or remediation explanation (goals reopen)")
	fs.StringVar(rf.reasonFlag, "reopen-reason", "", "alias for --reason (goals reopen)")
	rf.claimImmediateFlag = fs.Bool("claim", false, "immediately claim goal upon reopening (goals reopen)")
	rf.stageFlag = fs.Bool("stage", false, "automatically git add goals/README.md on sync (goals)")
	rf.commitFlag = fs.String("commit", "", "git commit message to automatically commit goals/README.md on sync (goals)")
	fs.StringVar(rf.commitFlag, "m", "", "commit message for two-stage goal commit (goals commit)")
	fs.StringVar(rf.commitFlag, "message", "", "alias for -m (goals commit)")
	fs.StringVar(rf.commitFlag, "c", "", "alias for --commit (goals attribute)")
	rf.noBuildFlag = fs.Bool("no-build", false, "skip rebuild step during two-stage goal commit (goals commit)")
	rf.checkTasksFlag = fs.Bool("check-tasks", false, "auto-check unchecked non-attribution plan tasks before goal commit (goals commit)")
	rf.noSyncFlag = fs.Bool("no-sync", false, "audit goals without modifying goals/README.md (goals)")
	rf.manifestFlag = fs.String("manifest", "", "path to expected-state manifest file (goals validate)")
	rf.statsFlag = fs.Bool("stats", false, "display database metrics without vacuuming (vacuum) or tabular goals statistics (goals)")
	fs.BoolVar(rf.statsFlag, "analyze", false, "alias for --stats (vacuum)")
	rf.byFlag = fs.String("by", "", "superseding goal slug, author, or human approver (goals supersede, approve)")
	rf.rejectFlag = fs.Bool("reject", false, "reject approval request instead of approving (approve)")
	rf.evidenceFlag = fs.String("evidence", "", "evidence report or test log file to certify a goal (goals certify)")
	rf.fullFlag = fs.Bool("full", false, "ignore stored cursors and re-import all history, backfilling source provenance (sync, ingest); redraw the full view (goals stats live)")
	rf.forceFlag = fs.Bool("force", false, "force repack and vacuum even if fragmentation is low (vacuum)")
	fs.BoolVar(rf.forceFlag, "f", false, "shorthand for --force (vacuum)")
	rf.checkOnlyFlag = fs.Bool("check", false, "check for newer releases without installing (upgrade, update)")
	rf.sourceFlag = fs.Bool("source", false, "force rebuild from local source repository via ./build.sh (upgrade, update)")
	rf.repairFlag = fs.Bool("repair", false, "automatically repair FTS5 indexes and optimize storage (doctor)")
	if sub == "doctor" {
		rf.skillsFlag = fs.Bool("skills", false, "audit agent skills and MCP servers for bloat, dormancy, and token waste (doctor)")
		rf.mcpFlag = fs.Bool("mcp", false, "show only running MCP server freshness and restart guidance (doctor)")
		rf.memoryFlag = fs.Bool("memory", false, "show local memory adoption report and advisory thresholds (doctor)")
	} else {
		rf.skillsListFlag = fs.String("skills", "", "comma-separated skill slugs to include (skill apply/preview)")
		fs.StringVar(rf.skillsListFlag, "skill", "", "alias for --skills (skill apply/preview)")
	}
	rf.excludeFlag = fs.String("exclude", "", "comma-separated skill exclusion patterns (skill apply/preview/remove)")
	rf.covenantFlag = fs.Bool("covenant", false, "verify Digital Data Sovereignty Covenant invariants (local custody, read-only loaders, zero telemetry, secret redaction) (doctor)")
	rf.edgeFlag = fs.Bool("edge", false, "audit Cloudflare Workers test suites for cross-test state leakage and unisolated storage (doctor)")
	rf.debtFlag = fs.Bool("debt", false, "audit repository for tracked debt comments (shortcut/ponytail) with ceilings and upgrade triggers (doctor)")
	rf.scarTissueFlag = fs.Bool("scar-tissue", false, "audit git diff against historical architectural scar tissue and outlawed patterns (gate)")
	fs.BoolVar(rf.scarTissueFlag, "scar", false, "alias for --scar-tissue (gate)")
	rf.docCoverageFlag = fs.Bool("doc-coverage", false, "audit exported Go declarations for complete Go doc comments (gate)")
	fs.BoolVar(rf.docCoverageFlag, "doc-lint", false, "alias for --doc-coverage (gate)")
	rf.reportCardFlag = fs.Bool("report-card", false, "run local Go Report Card code quality analyzer (gate, reportcard)")
	fs.BoolVar(rf.reportCardFlag, "reportcard", false, "alias for --report-card")
	rf.coverageFlag = fs.Bool("coverage", false, "evaluate test statement coverage against quality thresholds (gate, coverage)")
	profileDefault := ""
	profileUsage := "path to custom coverage.out profile to inspect (coverage)"
	if sub == "mcp" {
		profileDefault = "continuity"
		profileUsage = "MCP server profile: continuity (6 core tools) or full (67 tools) (mcp)"
	}
	rf.profileFlag = fs.String("profile", profileDefault, profileUsage)
	rf.mcpSchemaFlag = fs.Bool("print-schema", false, "print the canonical MCP tool schema and exit (mcp)")
	rf.asOfFlag = fs.String("as-of", "", "point-in-time view using YYYY-MM-DD or RFC3339 (search, recall)")
	rf.postCommitFlag = fs.Bool("post-commit", false, "install the managed post-commit hook that records commit receipts (gate install)")
	rf.trustTargetFlag = fs.String("trust-target", "", "trust one full 40-character commit SHA for this gate run")
	rf.showConfigFlag = fs.Bool("show-config", false, "print the trust identity, targets, and sensitive set, then exit (gate)")
	rf.mcpTraceFlag = fs.Bool("trace-args", false, "append metadata-only MCP argument traces to the local trace file (mcp)")
	rf.preCommitFlag = fs.Bool("pre-commit", false, "run fast pre-commit checks (<3s changed-package tests, formatting, secrets) (gate)")
	fs.BoolVar(rf.preCommitFlag, "quick", false, "alias for --pre-commit (gate)")
	fs.BoolVar(rf.preCommitFlag, "fast", false, "alias for --pre-commit (gate)")
	rf.skipArchFlag = fs.Bool("skip-arch", false, "skip architectural sentry checks (SRP file weights, edge anti-patterns, fact grounding) (gate)")
	rf.budgetFlag = fs.String("budget", "", "target context token budget for compression and pruning (e.g. 8k, 16k, 32k) (prime)")
	rf.maxTokensFlag = fs.String("max-tokens", "", "target context token budget for multi-pressure degradation (e.g. 8k, 16k, 20000) (prime)")
	rf.pressureFlag = fs.String("pressure", "auto", "context pressure mode: auto, normal, watch, heavy, critical (prime)")
	rf.includeMarkdownFlag = fs.Bool("include-markdown", false, "include full rendered markdown string in JSON context output (prime)")
	rf.markdownFlag = &reportMarkdown{}
	fs.Var(rf.markdownFlag, "markdown", "alias for --include-markdown (prime); write a markdown continuity report with an optional path (report)")
	rf.cachePinnedFlag = fs.Bool("cache-pinned", false, "format priming output with cache-pinned prompt segregation (prime)")
	rf.primeAllGoalsFlag = fs.Bool("all-goals", false, "include all completed roadmap goals instead of aggregate count and bounded recent goals (prime)")
	rf.noMapFlag = fs.Bool("no-map", false, "disable automatic codebase symbol digest in prime output (prime)")
	fs.BoolVar(rf.noMapFlag, "disable-map", false, "alias for --no-map (prime)")
	rf.mapCharsFlag = fs.Int("map-chars", 2000, "maximum character budget for codebase symbol digest (prime)")
	rf.eventFlag = fs.String("event", "", "lifecycle event kind (hook)")
	rf.hookNoAutoHandoff = fs.Bool("no-auto-handoff", false, "disable automatic session-end handoff (hook)")
	rf.payloadFlag = fs.String("payload", "", "json payload for hook event (hook)")
	rf.drainFlag = fs.Bool("drain", false, "immediately drain spool queue into store (hook)")
	rf.cwdFlag = fs.String("cwd", "", "working directory for lifecycle event (hook)")
	rf.hookFlag = fs.Bool("hook", false, "install git pre-push hook (gate install)")
	if sub != "feedback" {
		rf.remoteFlag = fs.Bool("remote", false, "install git remote proxy (gate install)")
	}
	rf.forwardFlag = fs.String("forward", "origin", "forward push target remote upon green gate (gate pipeline)")
	rf.noHooksFlag = fs.Bool("no-hooks", false, "skip git hooks installation (sila install)")
	rf.noMCPFlag = fs.Bool("no-mcp", false, "skip MCP registration (sila install)")
	rf.shimFlag = fs.Bool("shim", false, "provision standalone workspace binary runner in .bin/sila for sandboxed agents (sila install)")
	rf.runFlag = fs.Bool("run", false, "execute targeted test command immediately (test selector)")
	rf.dryRunFlag = fs.Bool("dry-run", false, "simulate operation and preview planned changes without mutating database (sync, dream, install)")
	fs.BoolVar(rf.dryRunFlag, "preview", false, "alias for --dry-run (sync, dream, install)")
	fs.BoolVar(rf.dryRunFlag, "n", false, "shorthand for --dry-run (sync, dream, install)")
	rf.cloudSyncFlag = fs.Bool("cloud", false, "pull from edge before sync and push verified knowledge after sync (sync)")
	fs.BoolVar(rf.cloudSyncFlag, "with-cloud", false, "alias for --cloud (sync)")
	rf.toolsFlag = fs.String("tools", "all", "comma-separated tool targets (sila install)")
	rf.replaceFlag = fs.Bool("replace", false, "authorize replacing existing user-owned files, e.g. a custom CLAUDE.md (sila install)")
	rf.policyPackFlag = fs.String("policy-pack", "", "opt-in policy pack to embed in compiled rules, e.g. maintainer (sila install)")
	rf.omniFlag = fs.Bool("omni", false, "multi-source hybrid search across facts, transcripts, git, docs (search)")
	rf.aclScopedFlag = fs.Bool("acl-scoped", false, "restrict cross-project results to the requester project plus global records (search --omni, recall)")
	rf.semanticFlag = fs.Bool("semantic", false, "enable System One semantic assistance (search rerank, reconcile verification)")
	fs.BoolVar(rf.semanticFlag, "rerank", false, "alias for --semantic")
	rf.windowFlag = fs.Int("window", 3, "number of preceding and following turns to retrieve (search expand)")
	rf.occurrenceFlag = fs.Int("occurrence", 0, "select among multiple recorded source occurrences of a message, 1-based (search expand)")
	rf.twoPassFlag = fs.Bool("two-pass", true, "enable two-pass asymmetric retrieval for message search (search)")
	rf.noTwoPassFlag = fs.Bool("no-two-pass", false, "disable two-pass search and use raw single-turn matching (search)")
	rf.target = fs.String("target", "claude", "target rule format: claude, cursor, windsurf, agents (export rules, agents compile)")
	if sub != "goals" {
		rf.reviewFlag = fs.Bool("review", false, "preview proposed instruction updates based on recent session evidence (export rules, agents review)")
	} else {
		falseVal := false
		rf.reviewFlag = &falseVal
	}
	rf.scopeFlag = fs.String("scope", "project", "instruction review scope: project (default) or global (export rules, agents review)")
	rf.portFlag = fs.Int("port", 7452, "HTTP server port (ui, web, demo)")
	rf.hostFlag = fs.String("host", "127.0.0.1", "HTTP server host binding (ui, web, demo)")
	rf.exposeFlag = fs.Bool("expose", false, "authorize non-loopback --host binding (ui, web, demo). Mutations then require the printed launch token; reads are reachable by anyone who can reach the port, so only expose a store you are willing to serve over the network")
	rf.noOpenFlag = fs.Bool("no-open", false, "do not automatically launch system browser (ui, web, demo)")
	rf.demoFlag = fs.Bool("demo", false, "launch the ephemeral demo playground with a pre-populated sandbox store (ui, web)")
	rf.webFlag = fs.Bool("web", false, "launch the HTTP Web Cockpit instead of the terminal walkthrough (demo)")
	rf.keepFlag = fs.Bool("keep", false, "keep the demo sandbox on disk for live exploration (demo)")
	rf.stepFlag = fs.String("step", "all", "comma-separated walkthrough stages: sync,salam,prime,recall,facts,gate,standup,all (demo)")
	rf.outFile = fs.String("out", "", "export/intro destination file (default stdout)")
	rf.memoryFileFlag = fs.String("file", "", "JSON memory envelope file for sila memory add/validate (- reads stdin)")
	rf.archiveAuthorityFlag = fs.String("authority", "", "archive or restore authority: user_request, agent_verified, sensitive (memory archive/restore)")
	rf.duplicateOfFlag = fs.String("duplicate-of", "", "duplicate target memory id (memory archive --reason=duplicate)")
	rf.hardDeleteFlag = fs.Bool("hard-delete", false, "refused: memory archive never deletes content")
	rf.revealSensitiveFlag = fs.Bool("reveal-sensitive", false, "reveal sensitive archive content on a TTY (memory archive list/history)")
	rf.includeArchivedFlag = fs.Bool("include-archived", false, "include archived and superseded memories (recall, prime, search, memory list)")
	rf.verifyFlag = fs.Bool("verify", false, "verify the lifecycle hash chain (memory history)")
	fs.BoolVar(rf.verifyFlag, "verify-chain", false, "alias for --verify (memory history)")
	rf.dbPath = fs.String("db", store.DefaultDBPath(), "knowledge store path")
	rf.revealFlag = fs.Bool("reveal", false, "reveal secrets unmasked in output (config)")
	rf.formatFlag = fs.String("format", "auto", "documentation export format: auto, notion, confluence, gitbook (doc)")
	rf.heavyCleanFlag = fs.Bool("heavy", false, "include heavy or network-dependent caches such as modcache or node_modules (clean)")
	fs.BoolVar(rf.heavyCleanFlag, "deep", false, "alias for --heavy (clean)")

	// Harness & eval flags
	if sub == "skill" || sub == "skills" {
		rf.harnessFlag = fs.String("harness", "antigravity", "target harness: antigravity (default) or gemini (skill preview/apply/remove)")
	} else {
		rf.harnessFlag = fs.String("harness", "auto", "harness runner: auto, claude, agy, mock (eval)")
	}
	rf.ablationFlag = fs.Bool("ablation", false, "run ablation study comparing with vs without target (eval)")
	rf.evalDirFlag = fs.String("eval-dir", "evals", "directory containing test cases (eval)")
	rf.fromLessonFlag = fs.String("from-lesson", "", "fact ID of recorded lesson to synthesize (eval)")
	rf.fromFeedbackFlag = fs.String("from-feedback", "", "feedback record ID to synthesize (eval)")
	rf.allLessonsFlag = fs.Bool("all-lessons", false, "synthesize tests for all recorded project lessons (eval)")
	rf.caseFlag = fs.String("case", "", "filter to specific case name (eval)")
	rf.bareFlag = fs.Bool("bare", false, "scaffold only prompt.md without starter graders (eval init)")
	rf.reportFlag = fs.Bool("report", false, "generate HTML dashboard at evals/report.html (eval)")

	// Release preflight flags
	rf.allowDirtyFlag = fs.Bool("allow-dirty", false, "allow running checks on uncommitted worktree (release preflight)")
	rf.offlineFlag = fs.Bool("offline", false, "force offline mode skipping remote probes (release preflight)")

	// Audit flags
	rf.promptOnlyFlag = fs.Bool("prompt-only", false, "generate turnkey multi-agent prompt markdown without simulated run (audit)")
	rf.registerGoalsFlag = fs.Bool("register-goals", false, "persist top synthesized consensus proposals directly into goals/<slug>/ (audit)")
	rf.squadFlag = fs.String("squad", "", "preset squad template or comma-separated persona IDs (audit)")
	rf.personasFlag = fs.String("personas", "", "comma-separated persona IDs to assemble for audit (audit)")
	rf.pagesFlag = fs.String("pages", "", "comma-separated sub-pages to scope for audit (audit)")
	rf.appFlag = fs.String("app", "", "application name under test (audit)")
	rf.recommendFlag = fs.String("recommend", "", "URL to recommend an audit squad for (audit squad)")
	rf.presetFlag = fs.String("preset", "", "preset squad name to view (audit squad)")
	rf.registerFlag = fs.Bool("register", false, "register detected gap personas into local/edge registry (audit gap)")
	rf.contextFlag = fs.String("context", "", "business or domain context for audit and squad recommendation (audit)")
	rf.groupFlag = fs.String("group", "", "filter personas by group ID or slug (persona list)")
	rf.interactiveFlag = fs.Bool("interactive", false, "conduct 4-question interactive terminal interview in CLI (skill-create)")
	rf.upstreamsFlag = fs.Bool("upstreams", false, "enable upstream MCP servers (mcp)")
	rf.noUpstreamsFlag = fs.Bool("no-upstreams", false, "disable upstream MCP servers (mcp)")

	// Symbol-pinned note flags
	rf.symbolFlag = fs.String("symbol", "", "target symbol name or canonical ID (note, recall, edit-check, safe-delete)")
	fs.StringVar(rf.symbolFlag, "sym", "", "alias for --symbol (note, recall)")

	// Context-aware recall flags
	rf.recallPathsFlag = fs.String("paths", "", "comma-separated repository-relative paths in play (recall)")
	rf.recallSymbolsFlag = fs.String("symbols", "", "comma-separated symbol names in play (recall)")
	rf.recallHeadFlag = fs.String("head", "", "git HEAD commit for context scoring (recall)")
	rf.recallContextFile = fs.String("context-file", "", "JSON file containing a recall context object (recall)")
	rf.kindFlag = fs.String("kind", "memo", "note kind: memo, lesson, invariant (note add)")
	fs.StringVar(rf.kindFlag, "k", "memo", "shorthand for --kind (note add)")
	rf.exportFlag = fs.Bool("export", false, "export symbol notes to .sila/notes.json (note sync)")
	fs.BoolVar(rf.exportFlag, "e", false, "shorthand for --export (note sync)")
	rf.importFlag = fs.Bool("import", false, "import symbol notes from .sila/notes.json (note sync)")
	fs.BoolVar(rf.importFlag, "i", false, "shorthand for --import (note sync)")

	// Crew & lane flags
	rf.briefFlag = fs.String("brief", "", "path to task brief file (crew plan-lanes)")
	rf.surfaceFlag = fs.String("surface", "", "comma-separated exclusive write surface patterns (crew spawn)")

	// Reserved CCLI control flags (registered via internal/cli)
	rf.noCCLIFlag = fs.Bool("no-ccli", false, "disable conversational CLI phrase rewriting for this invocation")
	rf.ccliStrictExitFlag = fs.Bool("ccli-strict-exit", false, "enforce strict CCLI exit codes")
	rf.noInputFlag = fs.Bool("no-input", false, "fail closed instead of prompting on interactive confirmation")
	rf.confirmFlag = fs.String("confirm", "", "explicit target confirmation token for destructive operations")

	return rf
}

// stringFlagValue returns the pointed-to string, or "" for a nil flag.
func stringFlagValue(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// visitedFlagState records which root flags were explicitly set.
func (rf *rootFlags) visitedFlagState(fs *flag.FlagSet) (top, target, kind, harness, db, olderThan, limit bool, interval string) {
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "top":
			top = true
		case "limit":
			limit = true
		case "harness":
			harness = true
		case "interval":
			interval = *rf.intervalFlag
		case "target":
			target = true
		case "db":
			db = true
		case "kind", "k":
			kind = true
		case "older-than":
			olderThan = true
		}
	})
	return
}
