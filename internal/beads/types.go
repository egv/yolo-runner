package beads

type Issue struct {
	ID                 string  `json:"id"`
	Title              string  `json:"title"`
	Description        string  `json:"description"`
	AcceptanceCriteria string  `json:"acceptance_criteria"`
	Status             string  `json:"status"`
	Priority           *int    `json:"priority"`
	IssueType          string  `json:"issue_type"`
	Children           []Issue `json:"children"`
}

type Bead struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"`
}
