package cli

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/term"

	"github.com/donwellus/aiw/internal/pipeline"
	"github.com/donwellus/aiw/internal/state"
)

type statusRow struct {
	ID          string
	Status      string
	Stage       string
	RunID       string
	CostUSD     float64
	Source      string
	PR          string
	UpdatedAt   time.Time
	Title       string
	Description string
}

type statusColumn struct {
	header   string
	width    int
	optional string
	value    func(statusRow) string
}

func statusRowsFromTasks(records []state.TaskRecord, runs map[string]*pipeline.RunState) []statusRow {
	rows := make([]statusRow, 0, len(records))
	for _, record := range records {
		row := statusRow{
			ID:        record.ID,
			Source:    record.Source,
			Status:    record.Status,
			RunID:     record.RunID,
			UpdatedAt: record.UpdatedAt,
		}
		if run := runs[record.RunID]; run != nil {
			row.Status = run.Status
			row.Stage = run.Stage
			row.CostUSD = run.Usage.CostUSD
			row.Source = run.Source
			row.Title = run.Task.Title
			row.Description = run.Task.Description
			row.PR = prLabel(run.PRURL)
			row.UpdatedAt = run.UpdatedAt
		}
		if strings.TrimSpace(row.Stage) == "" {
			row.Stage = "-"
		}
		if strings.TrimSpace(row.Title) == "" {
			row.Title = "-"
		}
		rows = append(rows, row)
	}
	return rows
}

func renderStatusTable(out io.Writer, rows []statusRow, width int) {
	if width <= 0 {
		width = 120
	}
	columns, titleWidth := statusLayout(rows, width)
	widths := make([]int, len(columns))
	for i := range columns {
		widths[i] = columns[i].width
	}
	headers := make([]string, len(columns))
	for i := range columns {
		headers[i] = columns[i].header
	}
	titleHeader := "TÍTULO / DESCRIÇÃO"
	if runeLen(titleHeader) > titleWidth {
		titleHeader = truncateStatusText(titleHeader, titleWidth)
	}
	headers[len(headers)-1] = titleHeader
	printStatusLine(out, headers, widths)
	for _, row := range rows {
		cells := make([]string, len(columns))
		for column := 0; column < len(columns)-1; column++ {
			cells[column] = columns[column].value(row)
		}
		cells[len(columns)-1] = statusRowText(row, titleWidth)
		printStatusLine(out, cells, widths)
	}

}

func statusLayout(rows []statusRow, width int) ([]statusColumn, int) {
	columns := []statusColumn{
		{header: "TAREFA", width: 6, value: func(row statusRow) string { return row.ID }},
		{header: "STATUS", width: 8, value: func(row statusRow) string { return row.Status }},
		{header: "ESTÁGIO", width: 9, value: func(row statusRow) string { return row.Stage }},
		{header: "CUSTO", width: 10, value: func(row statusRow) string { return fmt.Sprintf("US$ %.4f", row.CostUSD) }},
	}
	for i := range columns {
		columns[i].width = max(columns[i].width, runeLen(columns[i].header))
		for _, row := range rows {
			columns[i].width = max(columns[i].width, runeLen(columns[i].value(row)))
		}
	}
	optional := []statusColumn{
		{header: "RUN", optional: "run", width: 34, value: func(row statusRow) string { return row.RunID }},
		{header: "FONTE", optional: "source", width: 8, value: func(row statusRow) string { return row.Source }},
		{header: "ATUALIZADO", optional: "updated", width: 16, value: func(row statusRow) string {
			if row.UpdatedAt.IsZero() {
				return "-"
			}
			return row.UpdatedAt.Local().Format("2006-01-02 15:04")
		}},
		{header: "PR", optional: "pr", width: 6, value: func(row statusRow) string { return row.PR }},
	}
	for i := range optional {
		optional[i].width = max(optional[i].width, runeLen(optional[i].header))
		for _, row := range rows {
			optional[i].width = max(optional[i].width, runeLen(optional[i].value(row)))
		}
	}
	baseWidth := statusColumnsWidth(columns)
	for _, column := range optional {
		candidateWidth := baseWidth + column.width + 2
		if width-candidateWidth >= 60 {
			columns = append(columns, column)
			baseWidth = candidateWidth
		}
	}
	titleWidth := width - baseWidth - 2
	if titleWidth < 20 {
		titleWidth = 20
	}
	columns = append(columns, statusColumn{header: "TÍTULO / DESCRIÇÃO", width: titleWidth, value: func(row statusRow) string { return statusRowText(row, titleWidth) }})
	return columns, titleWidth
}

func statusColumnsWidth(columns []statusColumn) int {
	total := 0
	for i, column := range columns {
		if i > 0 {
			total += 2
		}
		total += column.width
	}
	return total
}

func printStatusLine(out io.Writer, cells []string, widths []int) {
	for i, cell := range cells {
		if i > 0 {
			fmt.Fprint(out, "  ")
		}
		if i == len(cells)-1 {
			fmt.Fprintln(out, cell)
			continue
		}
		fmt.Fprint(out, padStatus(cell, widths[i]))
	}
}

func padStatus(value string, width int) string {
	if runeLen(value) >= width {
		return value
	}
	return value + strings.Repeat(" ", width-runeLen(value))
}

func truncateStatusText(text string, width int) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return "-"
	}
	if width <= 0 || runeLen(text) <= width {
		return text
	}
	if width <= 1 {
		return "…"
	}
	return string([]rune(text)[:width-1]) + "…"
}

func statusRowText(row statusRow, width int) string {
	title := statusTitle(row.Title)
	description := statusDescription(row.Description)
	if description == "" {
		return truncateStatusText(title, width)
	}
	if width <= 0 {
		return title + " — " + description
	}
	if width < 12 {
		return truncateStatusText(title+" — "+description, width)
	}
	titleBudget := width * 45 / 100
	if titleBudget < 8 {
		titleBudget = 8
	}
	descriptionBudget := width - titleBudget - 3
	if descriptionBudget < 8 {
		descriptionBudget = 8
		titleBudget = width - descriptionBudget - 3
	}
	if titleBudget < 1 {
		titleBudget = 1
	}
	if runeLen(title) <= titleBudget {
		descriptionBudget = width - runeLen(title) - 3
		if descriptionBudget < 1 {
			return truncateStatusText(title+" — "+description, width)
		}
		return title + " — " + truncateStatusText(description, descriptionBudget)
	}
	return truncateStatusText(title, titleBudget) + " — " + truncateStatusText(description, descriptionBudget)
}

func statusTitle(title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if title == "" {
		return "-"
	}
	return title
}

func statusDescription(description string) string {
	return strings.Join(strings.Fields(description), " ")
}

func prLabel(url string) string {
	url = strings.TrimSpace(url)
	if url == "" {
		return "-"
	}
	index := strings.LastIndex(url, "/")
	if index >= 0 && index+1 < len(url) {
		return "#" + url[index+1:]
	}
	return url
}

func runeLen(value string) int {
	return utf8.RuneCountInString(value)
}

func terminalWidth(out io.Writer) int {
	if file, ok := out.(*os.File); ok && term.IsTerminal(int(file.Fd())) {
		if width, _, err := term.GetSize(int(file.Fd())); err == nil && width > 0 {
			return width
		}
	}
	if value, err := strconv.Atoi(strings.TrimSpace(os.Getenv("COLUMNS"))); err == nil && value > 0 {
		return value
	}
	return 120
}
