package model

// JiraIssue represents a Jira issue response
type JiraIssue struct {
	Key    string     `json:"key"`
	Fields JiraFields `json:"fields"`
}

// JiraFields represents the fields in a Jira issue
type JiraFields struct {
	Summary     string     `json:"summary"`
	Status      JiraStatus `json:"status"`
	Description string     `json:"description"`
	Assignee    JiraUser   `json:"assignee"`
}

// JiraStatus represents the status of a Jira issue
type JiraStatus struct {
	Name string `json:"name"`
}

// JiraUser represents a Jira user
type JiraUser struct {
	DisplayName string `json:"displayName"`
}

// JiraSearchResponse represents the response from a Jira search
type JiraSearchResponse struct {
	StartAt    int         `json:"startAt"`
	MaxResults int         `json:"maxResults"`
	Total      int         `json:"total"`
	Issues     []JiraIssue `json:"issues"`
}
