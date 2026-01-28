package opencode

type VerificationError struct {
	Reason string
}

func (err *VerificationError) Error() string {
	if err == nil {
		return "verification failed"
	}
	if err.Reason == "" {
		return "verification failed"
	}
	return "verification failed: " + err.Reason
}
