package contracts

// TranscriptionMutation is an unregistered internal adapter request. Future executors must
// authorize the account, bytes/task and resource-consuming effect separately.
// Data is supplied through the bounded upload channel, never an upstream URL.
type TranscriptionMutation struct {
	Action   string
	TaskRef  string
	Filename string
	MIME     string
	Data     []byte
}
type TranscriptionMutationResult struct {
	Action  string `json:"action"`
	TaskRef string `json:"task_ref"`
	Status  string `json:"status"`
}
