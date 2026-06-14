package arcanum

type PRSummary struct {
	ID         string
	Title      string
	Summary    string
	Author     string
	Reviewers  []string
	Branch     string
	FromBranch string
	FromID     string
	ToBranch   string
	Status     string
	Issues     []string
}
