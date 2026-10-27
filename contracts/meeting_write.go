package contracts

// MeetingMutation is an unregistered adapter contract. A future executor must
// validate the application grant, fair-use admission and trusted request-bound
// approval before using it. No arbitrary fields, recipients or URLs are accepted.
type MeetingMutation struct {
	Action          string `json:"action"`
	MeetingRef      string `json:"meeting_ref,omitempty"`
	GroupRef        string `json:"group_ref,omitempty"`
	Topic           string `json:"topic,omitempty"`
	Date            string `json:"date,omitempty"`
	Time            string `json:"time,omitempty"`
	DurationMinutes int    `json:"duration_minutes,omitempty"`
	Passcode        string `json:"passcode,omitempty"`
}
type MeetingMutationResult struct {
	Action     string `json:"action"`
	MeetingRef string `json:"meeting_ref"`
	Status     string `json:"status"`
}
