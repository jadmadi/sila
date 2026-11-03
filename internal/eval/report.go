package eval

import (
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// GenerateHTMLReport produces a self-contained, interactive HTML report of the evaluation run.
func GenerateHTMLReport(res *SuiteResult, outputPath string) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	passRate := 0.0
	if res.TotalCases > 0 {
		passRate = float64(res.PassedCases) / float64(res.TotalCases) * 100.0
	}

	var sb strings.Builder
	sb.WriteString(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="UTF-8">
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<title>Sila Epistemic Evaluation Report</title>
<style>
  :root {
    --bg: #0f172a;
    --card-bg: #1e293b;
    --text: #f8fafc;
    --text-muted: #94a3b8;
    --border: #334155;
    --accent: #38bdf8;
    --success: #22c55e;
    --warning: #f59e0b;
    --danger: #ef4444;
  }
  body {
    font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
    background: var(--bg);
    color: var(--text);
    margin: 0;
    padding: 24px;
    line-height: 1.5;
  }
  .container { max-width: 1200px; margin: 0 auto; }
  header { margin-bottom: 32px; border-bottom: 1px solid var(--border); padding-bottom: 16px; }
  h1 { margin: 0 0 8px 0; font-size: 28px; color: var(--accent); }
  .meta { color: var(--text-muted); font-size: 14px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(200px, 1fr)); gap: 16px; margin-bottom: 32px; }
  .card { background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; padding: 16px; }
  .card-val { font-size: 28px; font-weight: bold; margin-top: 4px; }
  .table-box { background: var(--card-bg); border: 1px solid var(--border); border-radius: 8px; overflow: hidden; margin-bottom: 32px; }
  table { width: 100%; border-collapse: collapse; text-align: left; }
  th, td { padding: 12px 16px; border-bottom: 1px solid var(--border); }
  th { background: #162032; font-weight: 600; color: var(--text-muted); font-size: 13px; text-transform: uppercase; }
  tr:last-child td { border-bottom: none; }
  .badge { display: inline-block; padding: 2px 8px; border-radius: 4px; font-size: 12px; font-weight: bold; }
  .badge-pass { background: rgba(34, 197, 94, 0.2); color: var(--success); }
  .badge-fail { background: rgba(239, 68, 68, 0.2); color: var(--danger); }
  .badge-keep { background: rgba(56, 189, 248, 0.2); color: var(--accent); }
  .badge-prune { background: rgba(239, 68, 68, 0.2); color: var(--danger); }
  .badge-demote { background: rgba(245, 158, 11, 0.2); color: var(--warning); }
  pre { background: #0b1120; padding: 12px; border-radius: 6px; overflow-x: auto; font-size: 13px; color: #e2e8f0; }
  details { margin-top: 8px; }
  summary { cursor: pointer; color: var(--accent); font-size: 13px; }
</style>
</head>
<body>
<div class="container">
  <header>
    <h1>صِلَة · Sila Epistemic Evaluation Report</h1>
    <div class="meta">Run Time: `)
	sb.WriteString(res.Timestamp.Format(time.RFC1123))
	sb.WriteString(` · Harness: <strong>`)
	sb.WriteString(html.EscapeString(res.Harness))
	sb.WriteString(`</strong> · Suite: <code>`)
	sb.WriteString(html.EscapeString(res.SuiteDir))
	sb.WriteString(`</code></div>
  </header>

  <div class="grid">
    <div class="card">
      <div class="meta">Total Cases</div>
      <div class="card-val">`)
	sb.WriteString(fmt.Sprintf("%d", res.TotalCases))
	sb.WriteString(`</div>
    </div>
    <div class="card">
      <div class="meta">Pass Rate</div>
      <div class="card-val" style="color: `)
	if passRate >= 80 {
		sb.WriteString("var(--success)")
	} else {
		sb.WriteString("var(--warning)")
	}
	sb.WriteString(fmt.Sprintf(`">%.1f%%</div>
    </div>
    <div class="card">
      <div class="meta">Total Score</div>
      <div class="card-val">%.1f / %.1f</div>
    </div>
    <div class="card">
      <div class="meta">Execution Duration</div>
      <div class="card-val">%s</div>
    </div>
  </div>
`, passRate, res.TotalScore, res.MaxScore, res.Duration.Round(time.Millisecond).String()))

	// Ablation Table
	if len(res.Ablations) > 0 {
		sb.WriteString(`
  <h2>Context & Rule Ablation Analysis</h2>
  <div class="table-box">
    <table>
      <thead>
        <tr>
          <th>Case</th>
          <th>Target</th>
          <th>Score (With)</th>
          <th>Score (Without)</th>
          <th>Ablation Delta</th>
          <th>Token Delta</th>
          <th>Token ROI (Per 1k)</th>
          <th>Verdict</th>
        </tr>
      </thead>
      <tbody>
`)
		for _, ab := range res.Ablations {
			sb.WriteString("        <tr>\n")
			sb.WriteString(fmt.Sprintf("          <td><code>%s</code></td>\n", html.EscapeString(ab.CaseID)))
			targetHtml := fmt.Sprintf("<code>%s</code>", html.EscapeString(ab.Target))
			if ab.TargetHash != "" {
				hashShort := ab.TargetHash
				if len(hashShort) > 12 {
					hashShort = hashShort[:12]
				}
				targetHtml += fmt.Sprintf("<div class=\"meta\">Hash: <code>%s</code></div>", html.EscapeString(hashShort))
			}
			if len(ab.ControlHashes) > 0 {
				targetHtml += fmt.Sprintf("<div class=\"meta\">Controls: %d verified</div>", len(ab.ControlHashes))
			}
			sb.WriteString(fmt.Sprintf("          <td>%s</td>\n", targetHtml))
			sb.WriteString(fmt.Sprintf("          <td>%.2f</td>\n", ab.ScoreWith))
			sb.WriteString(fmt.Sprintf("          <td>%.2f</td>\n", ab.ScoreWithout))

			deltaColor := "var(--text-muted)"
			if ab.Delta > 0 {
				deltaColor = "var(--success)"
			} else if ab.Delta < 0 {
				deltaColor = "var(--danger)"
			}
			sb.WriteString(fmt.Sprintf("          <td style=\"color:%s; font-weight:bold;\">%+.1f%%</td>\n", deltaColor, ab.Delta*100.0))

			tokTag := "est"
			if ab.TokensMeasured {
				tokTag = "measured"
			}
			sb.WriteString(fmt.Sprintf("          <td>%+d <span class=\"meta\">(%s)</span></td>\n", ab.TokenDelta, tokTag))
			if ab.Recommendation == "insufficient_evidence" {
				sb.WriteString("          <td><span class=\"meta\">n/a</span></td>\n")
			} else {
				sb.WriteString(fmt.Sprintf("          <td>%.2f</td>\n", ab.EfficacyPer1k))
			}

			badgeClass := "badge-keep"
			badgeText := strings.ToUpper(strings.ReplaceAll(ab.Recommendation, "_", " "))
			if ab.Recommendation == "prune" {
				badgeClass = "badge-prune"
			} else if ab.Recommendation == "demote" || ab.Recommendation == "insufficient_evidence" {
				badgeClass = "badge-demote"
			}
			reasonHtml := ""
			if ab.Reason != "" {
				reasonHtml = fmt.Sprintf("<div class=\"meta\">%s</div>", html.EscapeString(ab.Reason))
			}
			sb.WriteString(fmt.Sprintf("          <td><span class=\"badge %s\">%s</span>%s</td>\n", badgeClass, badgeText, reasonHtml))
			sb.WriteString("        </tr>\n")
		}
		sb.WriteString("      </tbody>\n    </table>\n  </div>\n")
	}

	// Test Cases Table
	sb.WriteString(`
  <h2>Evaluated Test Cases</h2>
  <div class="table-box">
    <table>
      <thead>
        <tr>
          <th>Case ID</th>
          <th>Status</th>
          <th>Score</th>
          <th>Pass Rate</th>
          <th>Duration</th>
          <th>Details</th>
        </tr>
      </thead>
      <tbody>
`)
	for _, c := range res.Cases {
		sb.WriteString("        <tr>\n")
		sb.WriteString(fmt.Sprintf("          <td><strong>%s</strong></td>\n", html.EscapeString(c.CaseID)))
		if c.Passed {
			sb.WriteString("          <td><span class=\"badge badge-pass\">PASS</span></td>\n")
		} else {
			sb.WriteString("          <td><span class=\"badge badge-fail\">FAIL</span></td>\n")
		}
		sb.WriteString(fmt.Sprintf("          <td>%.1f / %.1f</td>\n", c.TotalScore, c.MaxScore))
		sb.WriteString(fmt.Sprintf("          <td>%.1f%%</td>\n", c.PassRate*100.0))
		sb.WriteString(fmt.Sprintf("          <td>%s</td>\n", c.Duration.Round(time.Millisecond).String()))
		sb.WriteString("          <td>\n")

		// Graders
		for _, g := range c.Grades {
			gBadge := "<span class=\"badge badge-pass\">PASS</span>"
			if g.Unavailable {
				gBadge = "<span class=\"badge badge-demote\">UNAVAILABLE</span>"
			} else if !g.Passed {
				gBadge = "<span class=\"badge badge-fail\">FAIL</span>"
			}
			typeStr := g.GraderType
			if g.JudgeModel != "" {
				typeStr += fmt.Sprintf(" [%s]", g.JudgeModel)
			}
			sb.WriteString(fmt.Sprintf("            <div>%s <strong>%s</strong> (%s): %.1f/%.1f <span class=\"meta\">%s</span></div>\n",
				gBadge, html.EscapeString(g.GraderName), html.EscapeString(typeStr), g.Score, g.MaxScore, html.EscapeString(g.Detail)))
		}

		if c.RunResult != nil && c.RunResult.Output != "" {
			preview := c.RunResult.Output
			if len(preview) > 500 {
				preview = preview[:497] + "..."
			}
			sb.WriteString(fmt.Sprintf("            <details><summary>View Output Trace</summary><pre>%s</pre></details>\n", html.EscapeString(preview)))
		}

		sb.WriteString("          </td>\n        </tr>\n")
	}
	sb.WriteString(`      </tbody>
    </table>
  </div>
</div>
</body>
</html>`)

	return os.WriteFile(outputPath, []byte(sb.String()), 0644)
}

// WriteJSONReport outputs aggregate evaluation metrics in machine-readable JSON.
func WriteJSONReport(res *SuiteResult, outputPath string) error {
	dir := filepath.Dir(outputPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(outputPath, data, 0644)
}
