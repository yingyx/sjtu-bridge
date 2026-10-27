package contracts

// DataClass describes disclosed business data, never credentials. Sensitive
// reads require separate review; read-only does not mean safe to disclose.
type DataClass string

const (
	PublicData    DataClass = "public"
	PersonalData  DataClass = "personal"
	SensitiveData DataClass = "sensitive"
)

// Effect records additional consequences independently of a state-changing
// Operation. NoEffect must stand alone; write + none is still a business write.
type Effect string

const (
	NoEffect                 Effect = "none"
	CommunicationEffect      Effect = "communication"
	ResourceCommitmentEffect Effect = "resource_commitment"
	FormalSubmissionEffect   Effect = "formal_submission"
	FinancialEffect          Effect = "financial"
	AccessControlEffect      Effect = "access_control"
	DestructiveEffect        Effect = "destructive"
)

// Reversibility concerns business state, not recovery of disclosed data.
// Unknown must never be interpreted as reversible.
type Reversibility string

const (
	NotApplicable        Reversibility = "not_applicable"
	Reversible           Reversibility = "reversible"
	Compensatable        Reversibility = "compensatable"
	Irreversible         Reversibility = "irreversible"
	UnknownReversibility Reversibility = "unknown"
)

// ExecutionRule is a reviewed minimum requirement, not caller authorization.
// Confirm, manual and disabled are descriptive only: the MVP cannot execute
// them and has no trusted business approval workflow.
type ExecutionRule string

const (
	PublicExecution   ExecutionRule = "public"
	GrantExecution    ExecutionRule = "grant"
	ConfirmExecution  ExecutionRule = "confirm_each"
	ManualExecution   ExecutionRule = "manual_only"
	DisabledExecution ExecutionRule = "disabled"
)
