package contracts

// CredentialKind binds a reviewed provider to its stored credential type.
// It does not register a provider, capability, or permission.
func CredentialKind(provider string) (string, bool) {
	switch provider {
	case "canvas":
		return "CanvasToken", true
	case "jcourse":
		return "JCourseAPIKey", true
	case "shuiyuan":
		return "DiscourseUserAPIKey", true
	case "teaching", "sjtumail", "activity", "dekt", "electric", "bath", "pan", "mlearning", "campuscard", "calendar", "myportal", "meeting", "transcribe":
		return "JAAuthCookie", true
	default:
		return "", false
	}
}
