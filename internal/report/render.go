package report

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"gopkg.in/yaml.v3"
)

type Format string

const (
	FormatTable Format = "table"
	FormatJSON  Format = "json"
	FormatYAML  Format = "yaml"
	FormatMD    Format = "md"
	FormatHTML  Format = "html"
)

func Render(w io.Writer, format Format, report *Report) error {
	switch format {
	case FormatTable:
		return renderTable(w, report)
	case FormatJSON:
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		return encoder.Encode(report)
	case FormatYAML:
		encoder := yaml.NewEncoder(w)
		encoder.SetIndent(2)
		if err := encoder.Encode(report); err != nil {
			_ = encoder.Close()
			return err
		}
		return encoder.Close()
	case FormatMD:
		return renderMarkdown(w, report)
	case FormatHTML:
		return reportTemplate.Execute(w, report)
	default:
		return fmt.Errorf("formato de relatório desconhecido: %s", format)
	}
}

func renderTable(w io.Writer, report *Report) error {
	fmt.Fprintf(w, "Relatório aiw — %s\n", timestamp(report.GeneratedAt))
	fmt.Fprintf(w, "Projeto: %s\n", report.Root)
	fmt.Fprintf(w, "Pausa global: %s\n", yesNo(report.Paused))
	if report.Source != "" {
		fmt.Fprintf(w, "Snapshot da fonte: %s\n", report.Source)
	}
	fmt.Fprintln(w, "\nRESUMO")
	fmt.Fprintf(w, "  tarefas: %d\n", report.Summary.Tasks)
	fmt.Fprintf(w, "  status: %s\n", statusSummary(report.Summary))
	fmt.Fprintf(w, "  runs: %d\n", report.Summary.Runs)
	fmt.Fprintf(w, "  custo: US$ %.4f (todas as runs); US$ %.4f (última por tarefa)\n", report.Summary.CostUSD, report.Summary.TaskCostUSD)
	fmt.Fprintf(w, "  tokens: %d in / %d out\n", report.Summary.InputTokens, report.Summary.OutputTokens)
	fmt.Fprintf(w, "  claims: %d ativos (%d vencidos)\n", report.Summary.ActiveClaims, report.Summary.StaleClaims)

	fmt.Fprintln(w, "\nTAREFAS")
	writer := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(writer, "ID\tFONTE\tSTATUS\tLOCAL\tESTÁGIO\tÚLTIMA RUN\tCUSTO\tATUALIZADO\tTÍTULO")
	for _, task := range report.Tasks {
		fmt.Fprintf(writer, "%s\t%s\t%s\t%s\t%s\t%s\tUS$ %.4f\t%s\t%s\n",
			task.ID,
			firstNonEmpty(task.Source, "-"),
			task.Status,
			firstNonEmpty(task.Location, "-"),
			firstNonEmpty(task.Stage, "-"),
			firstNonEmpty(task.RunID, "-"),
			task.CostUSD,
			timestamp(task.UpdatedAt),
			compact(task.Title, 72),
		)
	}
	writer.Flush()

	if len(report.Claims) > 0 {
		fmt.Fprintln(w, "\nCLAIMS")
		claims := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
		fmt.Fprintln(claims, "TAREFA\tRUN\tPID\tIDADE\tVENCIDO")
		for _, claim := range report.Claims {
			fmt.Fprintf(claims, "%s\t%s\t%d\t%s\t%s\n",
				claim.TaskID,
				firstNonEmpty(claim.RunID, "-"),
				claim.PID,
				formatAge(claim.AgeSeconds),
				yesNo(claim.Stale),
			)
		}
		claims.Flush()
	}

	fmt.Fprintln(w, "\nALERTAS")
	if len(report.Alerts) == 0 {
		fmt.Fprintln(w, "  nenhum alerta")
		return nil
	}
	for _, alert := range report.Alerts {
		context := ""
		if alert.TaskID != "" {
			context = " tarefa=" + alert.TaskID
		}
		if alert.RunID != "" {
			context += " run=" + alert.RunID
		}
		fmt.Fprintf(w, "  [%s] %s%s: %s\n", alert.Level, alert.Kind, context, alert.Message)
	}
	return nil
}

func renderMarkdown(w io.Writer, report *Report) error {
	fmt.Fprintln(w, "# Relatório aiw")
	fmt.Fprintln(w)
	fmt.Fprintf(w, "- **Gerado em:** %s\n", timestamp(report.GeneratedAt))
	fmt.Fprintf(w, "- **Projeto:** `%s`\n", mdEscape(report.Root))
	fmt.Fprintf(w, "- **Pausa global:** %s\n", yesNo(report.Paused))
	if report.Source != "" {
		fmt.Fprintf(w, "- **Snapshot da fonte:** `%s`\n", mdEscape(report.Source))
	}

	fmt.Fprintln(w, "\n## Resumo")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Métrica | Valor |")
	fmt.Fprintln(w, "|---|---|")
	fmt.Fprintf(w, "| Tarefas | %d |\n", report.Summary.Tasks)
	fmt.Fprintf(w, "| Status | %s |\n", mdEscape(statusSummary(report.Summary)))
	fmt.Fprintf(w, "| Runs | %d |\n", report.Summary.Runs)
	fmt.Fprintf(w, "| Custo acumulado | US$ %.4f |\n", report.Summary.CostUSD)
	fmt.Fprintf(w, "| Custo da última run por tarefa | US$ %.4f |\n", report.Summary.TaskCostUSD)
	fmt.Fprintf(w, "| Tokens | %d in / %d out |\n", report.Summary.InputTokens, report.Summary.OutputTokens)
	fmt.Fprintf(w, "| Claims | %d ativos, %d vencidos |\n", report.Summary.ActiveClaims, report.Summary.StaleClaims)

	fmt.Fprintln(w, "\n## Tarefas")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| ID | Fonte | Status | Local | Estágio | Última run | Custo | Atualizado | Título |")
	fmt.Fprintln(w, "|---|---|---|---|---|---|---|---|---|")
	for _, task := range report.Tasks {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | US$ %.4f | %s | %s |\n",
			mdEscape(task.ID),
			mdEscape(firstNonEmpty(task.Source, "-")),
			mdEscape(task.Status),
			mdEscape(firstNonEmpty(task.Location, "-")),
			mdEscape(firstNonEmpty(task.Stage, "-")),
			mdEscape(firstNonEmpty(task.RunID, "-")),
			task.CostUSD,
			timestamp(task.UpdatedAt),
			mdEscape(task.Title),
		)
	}
	if len(report.Tasks) == 0 {
		fmt.Fprintln(w, "| - | - | - | - | - | - | - | - | Nenhuma tarefa |")
	}

	if len(report.Claims) > 0 {
		fmt.Fprintln(w, "\n## Claims")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Tarefa | Run | PID | Idade | Vencido |")
		fmt.Fprintln(w, "|---|---|---|---|---|")
		for _, claim := range report.Claims {
			fmt.Fprintf(w, "| %s | %s | %d | %s | %s |\n",
				mdEscape(claim.TaskID),
				mdEscape(firstNonEmpty(claim.RunID, "-")),
				claim.PID,
				formatAge(claim.AgeSeconds),
				yesNo(claim.Stale),
			)
		}
	}

	fmt.Fprintln(w, "\n## Alertas")
	fmt.Fprintln(w)
	if len(report.Alerts) == 0 {
		fmt.Fprintln(w, "Nenhum alerta.")
		return nil
	}
	for _, alert := range report.Alerts {
		context := ""
		if alert.TaskID != "" {
			context = fmt.Sprintf(" `%s`", mdEscape(alert.TaskID))
		}
		if alert.RunID != "" {
			context += fmt.Sprintf(" `%s`", mdEscape(alert.RunID))
		}
		fmt.Fprintf(w, "- **[%s]** `%s`%s: %s\n", alert.Level, alert.Kind, context, mdEscape(alert.Message))
	}
	return nil
}

var reportTemplate = template.Must(template.New("report").Funcs(template.FuncMap{
	"cost":        func(value float64) string { return fmt.Sprintf("US$ %.4f", value) },
	"time":        timestamp,
	"age":         formatAge,
	"yesno":       yesNo,
	"statusClass": statusClass,
	"orDash":      func(value string) string { return firstNonEmpty(value, "-") },
}).Parse(htmlReportTemplate))

const htmlReportTemplate = `<!doctype html>
<html lang="pt-BR">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Relatório aiw</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; margin: 2rem auto; max-width: 1200px; padding: 0 1rem; color: #1f2933; background: #fafbfc; }
h1, h2 { margin-bottom: .4rem; }
h2 { margin-top: 2rem; border-bottom: 1px solid #d9e2ec; padding-bottom: .3rem; }
table { border-collapse: collapse; width: 100%; margin: .8rem 0; background: #fff; }
th, td { padding: .5rem .65rem; border-bottom: 1px solid #e4ebf2; text-align: left; vertical-align: top; }
th { background: #eef2f7; font-size: .8rem; text-transform: uppercase; letter-spacing: .03em; }
tr:hover td { background: #f8fafc; }
.muted { color: #6b7280; }
.status { display: inline-block; padding: .1rem .5rem; border-radius: 999px; font-size: .8rem; font-weight: 600; }
.status.succeeded { background: #d3f9d8; color: #1b5e20; }
.status.failed { background: #ffe3e3; color: #a30000; }
.status.running { background: #d0ebff; color: #0b4f91; }
.status.paused, .status.needs_input { background: #fff3bf; color: #7a4d00; }
.status.todo { background: #e9ecef; color: #343a40; }
.status.canceled { background: #e9ecef; color: #495057; }
.alert { padding: .55rem .8rem; margin: .4rem 0; border-left: 4px solid #f59f00; background: #fff9db; }
.alert.error { border-color: #e03131; background: #fff5f5; }
.empty { color: #6b7280; font-style: italic; }
</style>
</head>
<body>
<h1>Relatório aiw</h1>
<p class="muted">Gerado em {{time .GeneratedAt}} · {{.Root}}</p>
<p>Pausa global: <strong>{{yesno .Paused}}</strong>{{if .Source}} · Snapshot da fonte: <strong>{{.Source}}</strong>{{end}}</p>

<h2>Resumo</h2>
<table>
<tr><th>Métrica</th><th>Valor</th></tr>
<tr><td>Tarefas</td><td>{{.Summary.Tasks}}</td></tr>
<tr><td>Status</td><td>{{range $index, $status := .Summary.OrderedStatuses}}{{if $index}}, {{end}}{{$status.Status}} {{$status.Count}}{{end}}</td></tr>
<tr><td>Runs</td><td>{{.Summary.Runs}}</td></tr>
<tr><td>Custo acumulado</td><td>{{cost .Summary.CostUSD}}</td></tr>
<tr><td>Custo da última run por tarefa</td><td>{{cost .Summary.TaskCostUSD}}</td></tr>
<tr><td>Tokens</td><td>{{.Summary.InputTokens}} in / {{.Summary.OutputTokens}} out</td></tr>
<tr><td>Claims</td><td>{{.Summary.ActiveClaims}} ativos, {{.Summary.StaleClaims}} vencidos</td></tr>
</table>

<h2>Tarefas</h2>
{{if .Tasks}}
<table>
<tr><th>ID</th><th>Fonte</th><th>Status</th><th>Local</th><th>Estágio</th><th>Última run</th><th>Custo</th><th>Atualizado</th><th>Título</th></tr>
{{range .Tasks}}
<tr>
<td>{{.ID}}</td>
<td>{{orDash .Source}}</td>
<td><span class="status {{statusClass .Status}}">{{.Status}}</span></td>
<td>{{orDash .Location}}</td>
<td>{{orDash .Stage}}</td>
<td>{{orDash .RunID}}</td>
<td>{{cost .CostUSD}}</td>
<td>{{time .UpdatedAt}}</td>
<td>{{.Title}}</td>
</tr>
{{end}}
</table>
{{else}}
<p class="empty">Nenhuma tarefa registrada.</p>
{{end}}

{{if .Claims}}
<h2>Claims</h2>
<table>
<tr><th>Tarefa</th><th>Run</th><th>PID</th><th>Idade</th><th>Vencido</th></tr>
{{range .Claims}}
<tr><td>{{.TaskID}}</td><td>{{orDash .RunID}}</td><td>{{.PID}}</td><td>{{age .AgeSeconds}}</td><td>{{yesno .Stale}}</td></tr>
{{end}}
</table>
{{end}}

<h2>Alertas</h2>
{{if .Alerts}}
{{range .Alerts}}
<div class="alert {{.Level}}"><strong>[{{.Level}}] {{.Kind}}</strong>{{if .TaskID}} · tarefa {{.TaskID}}{{end}}{{if .RunID}} · run {{.RunID}}{{end}}: {{.Message}}</div>
{{end}}
{{else}}
<p class="empty">Nenhum alerta.</p>
{{end}}
</body>
</html>
`

func statusSummary(summary Summary) string {
	statuses := summary.OrderedStatuses()
	if len(statuses) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(statuses))
	for _, status := range statuses {
		parts = append(parts, fmt.Sprintf("%s %d", status.Status, status.Count))
	}
	return strings.Join(parts, ", ")
}

func statusClass(status string) string {
	switch status {
	case "succeeded", "failed", "running", "paused", "needs_input", "todo", "canceled":
		return status
	default:
		return "todo"
	}
}

func timestamp(value time.Time) string {
	if value.IsZero() {
		return "-"
	}
	return value.Format("2006-01-02 15:04:05")
}

func yesNo(value bool) string {
	if value {
		return "sim"
	}
	return "não"
}

func compact(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	if limit <= 0 || len([]rune(value)) <= limit {
		return value
	}
	runes := []rune(value)
	return string(runes[:limit-1]) + "…"
}

func mdEscape(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, "|", "\\|")
	value = strings.ReplaceAll(value, "\r", "")
	value = strings.ReplaceAll(value, "\n", " ")
	return value
}
