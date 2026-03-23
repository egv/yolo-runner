package taskquality

import (
	"fmt"
	"regexp"
	"strings"
)

type TaskInput struct {
	Title               string
	Description         string
	AcceptanceCriteria  string
	Deliverables        string
	TestingPlan         string
	DefinitionOfDone    string
	DependenciesContext string
}

type Assessment struct {
	Score  int
	Issues []string
}

var vagueLanguagePattern = regexp.MustCompile(`(?:^|[\s\W])(maybe|consider)(?:[\s\W]|$)`)

type CoverageReport struct {
	TotalCases   int
	CoveredCases int
	MissingCases []string
	OverallScore int
}

func AssessTaskQuality(task TaskInput) Assessment {
	issues := []string{}
	score := 0

	score += requiredFieldScore(task, &issues)
	score += clarityScore(task, &issues)
	score += acceptanceCoverageScore(task, &issues)
	score += validationRigorScore(task, &issues)
	score += nonGoalRiskScore(task, &issues)
	score = clampScore(score)

	return Assessment{
		Score:  score,
		Issues: issues,
	}
}

func clampScore(score int) int {
	if score < 0 {
		return 0
	}
	if score > 100 {
		return 100
	}
	return score
}

func requiredFieldScore(task TaskInput, issues *[]string) int {
	required := []struct {
		label string
		value string
	}{
		{label: "Title", value: task.Title},
		{label: "Description", value: task.Description},
		{label: "Acceptance Criteria", value: task.AcceptanceCriteria},
		{label: "Deliverables", value: task.Deliverables},
		{label: "Testing Plan", value: task.TestingPlan},
		{label: "Definition of Done", value: task.DefinitionOfDone},
		{label: "Dependencies/Context", value: task.DependenciesContext},
	}

	present := 0
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			*issues = append(*issues, "missing required field: "+item.label)
			continue
		}
		present++
	}

	score := present * 3
	if present == len(required) {
		score += 2
	}
	return score
}

func clarityScore(task TaskInput, issues *[]string) int {
	passed := 0

	titleDesc := strings.ToLower(task.Title + " " + task.Description)
	if !containsAny(titleDesc, []string{"improve", "more robust", "handle all", "make better", "robustness"}) {
		passed++
	} else {
		*issues = append(*issues, "clarity: avoid vague wording")
	}

	if len(strings.TrimSpace(task.Description)) > 50 {
		passed++
	} else {
		*issues = append(*issues, "clarity: description should be at least 50 characters")
	}

	if hasVagueLanguage(task.Description) || hasVagueLanguage(task.Title) {
		*issues = append(*issues, "clarity: avoid vague wording")
	}

	if hasGivenWhenThen(task.AcceptanceCriteria) {
		passed++
	} else {
		*issues = append(*issues, "clarity: acceptance criteria lacks testable Given/When/Then statements")
	}

	if hasConcreteScope(task.Description, task.Deliverables) {
		passed++
	} else {
		*issues = append(*issues, "clarity: task scope should be explicit and testable")
	}

	if !containsAny(strings.ToLower(task.Description), []string{"best effort", "as needed", "as soon as possible"}) {
		passed++
	} else {
		*issues = append(*issues, "clarity: deterministic wording is required")
	}

	if hasDependencySignal(task.DependenciesContext) {
		passed++
	} else {
		*issues = append(*issues, "clarity: mention dependencies and context")
	}

	testingPlan := strings.ToLower(task.TestingPlan)
	if strings.TrimSpace(task.TestingPlan) != "" && (strings.Contains(testingPlan, "go test") || strings.Contains(testingPlan, "test")) {
		passed++
	} else {
		*issues = append(*issues, "clarity: include test-first intent in testing plan")
	}

	score := passed * 3
	if passed == 6 {
		score += 2
	}
	return score
}

func hasVagueLanguage(text string) bool {
	return vagueLanguagePattern.MatchString(strings.ToLower(strings.TrimSpace(text)))
}

func acceptanceCoverageScore(task TaskInput, issues *[]string) int {
	report := AcceptanceCoverageReport(task)
	if report.TotalCases == 0 {
		*issues = append(*issues, "acceptance criteria should exist and be explicit")
		return 0
	}

	score := 0
	if report.TotalCases > 0 {
		score += 10
	}

	if hasGivenWhenThen(task.AcceptanceCriteria) {
		score += 10
	} else {
		*issues = append(*issues, "acceptance criteria should use Given/When/Then format")
	}

	if report.CoveredCases < report.TotalCases {
		missing := report.TotalCases - report.CoveredCases
		*issues = append(*issues, fmt.Sprintf("acceptance criteria coverage gaps: missing %d of %d criteria in testing plan", missing, report.TotalCases))
		for _, missingCase := range report.MissingCases {
			*issues = append(*issues, "acceptance coverage missing case: "+missingCase)
		}
	}

	return score
}

func AcceptanceCoverageReport(task TaskInput) CoverageReport {
	cases := parseAcceptanceCriteria(task.AcceptanceCriteria)
	total := len(cases)
	if total == 0 {
		return CoverageReport{
			TotalCases:   0,
			CoveredCases: 0,
			OverallScore: 0,
		}
	}

	covered := concreteTestCommandCount(task.TestingPlan)
	if covered > total {
		covered = total
	}

	missingCount := total - covered
	missingCases := make([]string, 0, missingCount)
	if missingCount > 0 {
		missingCases = append(missingCases, cases[covered:]...)
	}

	return CoverageReport{
		TotalCases:   total,
		CoveredCases: covered,
		MissingCases: missingCases,
		OverallScore: (covered * 100) / total,
	}
}

func parseAcceptanceCriteria(raw string) []string {
	cases := []string{}
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		caseText := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "-"), "*"))
		if caseText == "" {
			continue
		}
		cases = append(cases, caseText)
	}
	return cases
}

func validationRigorScore(task TaskInput, issues *[]string) int {
	plan := strings.ToLower(strings.TrimSpace(task.TestingPlan))
	if plan == "" {
		*issues = append(*issues, "validation rigor: testing plan missing")
		return 0
	}

	score := 10
	if hasConcreteTestCommand(plan) {
		score += 10
	} else {
		*issues = append(*issues, "validation rigor: testing plan needs concrete command examples")
	}
	return score
}

func nonGoalRiskScore(task TaskInput, issues *[]string) int {
	context := strings.ToLower(task.DependenciesContext)
	if strings.Contains(context, "non-goals") ||
		strings.Contains(context, "risks") ||
		strings.Contains(context, "assumption") ||
		strings.Contains(context, "blocker") {
		return 20
	}
	*issues = append(*issues, "non-goals or risks should be explicitly documented")
	return 0
}

func hasGivenWhenThen(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "given") && strings.Contains(lower, "when") && strings.Contains(lower, "then")
}

func hasConcreteScope(description string, deliverables string) bool {
	combined := strings.ToLower(description + " " + deliverables)
	return strings.Contains(combined, "internal/") ||
		strings.Contains(combined, ".go") ||
		strings.Contains(combined, "file:") ||
		strings.Contains(combined, "/")
}

func hasDependencySignal(text string) bool {
	lower := strings.ToLower(text)
	return strings.Contains(lower, "depends on") ||
		strings.Contains(lower, "dependency") ||
		strings.Contains(lower, "assumption") ||
		strings.Contains(lower, "service") ||
		strings.Contains(lower, "tool")
}

func hasConcreteTestCommand(text string) bool {
	return strings.Contains(text, "go test") ||
		strings.Contains(text, "make ") ||
		strings.Contains(text, "pytest") ||
		strings.Contains(text, "npm test") ||
		strings.Contains(text, "go run")
}

func concreteTestCommandCount(plan string) int {
	lines := 0
	for _, line := range strings.Split(plan, "\n") {
		trimmed := strings.ToLower(strings.TrimSpace(line))
		if trimmed == "" {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(trimmed, "-"), "*"))
		if hasConcreteTestCommand(trimmed) {
			lines++
		}
	}
	return lines
}

func containsAny(haystack string, needles []string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}
